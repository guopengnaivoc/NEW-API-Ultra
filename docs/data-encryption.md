# Reversible secret encryption and key rotation

new-api encrypts reversible credentials in the primary database with a
dedicated, versioned AES-256-GCM envelope. Protected data includes channel
credentials, TOTP seeds, selected system options, custom OAuth client secrets,
and user notification settings containing a webhook secret or Gotify token.
Credential-bearing user settings are also encrypted before they are written to
Redis.

This keyring is independent of `SESSION_SECRET` and `CRYPTO_SECRET`. Never
reuse either value as a data-encryption key.

## Configuration

Generate a 32-byte key and encode it with standard Base64:

```sh
openssl rand -base64 32
```

Configure the resulting value with a short key identifier:

```sh
DATA_ENCRYPTION_KEYS=primary=<base64-encoded-32-byte-key>
DATA_ENCRYPTION_ACTIVE_KEY_ID=primary
DATA_ENCRYPTION_ENABLE=true
```

`DATA_ENCRYPTION_KEYS` accepts comma-separated
`key-id=base64-encoded-key` entries. Identifiers may contain ASCII letters,
digits, `_`, and `-`, and may be at most 32 characters. Every decoded key must
be exactly 32 bytes. All nodes connected to the same primary database or Redis
must receive the same complete keyring and active identifier.

The application rejects malformed, duplicate, partial, or unknown-key
configuration. It never falls back to the session secret, cache HMAC secret,
database password, or another application credential.

## Existing deployment upgrade

Encryption enforcement defaults to `true`. An existing deployment containing
protected plaintext values will fail startup unless it takes one of these two
safe actions before installing the new binary:

1. Configure `DATA_ENCRYPTION_KEYS` and
   `DATA_ENCRYPTION_ACTIVE_KEY_ID` on every node; or
2. Pre-set `DATA_ENCRYPTION_ENABLE=false` on every node for the staged
   all-nodes upgrade described below.

Use the staged path whenever old and new binaries may run at the same time:

1. Generate the keyring and distribute the same configuration to every node.
2. Set `DATA_ENCRYPTION_ENABLE=false` on every node before deploying the new
   binary.
3. Upgrade every node. New binaries can read legacy plaintext and existing
   envelopes, but database writes remain in the legacy format during this
   temporary phase. Credential-bearing user settings are still encrypted
   before Redis persistence.
4. Confirm that no old binary remains.
5. Enter maintenance mode and stop or drain all application traffic,
   background credential-refresh jobs, and every process that still has
   `DATA_ENCRYPTION_ENABLE=false`. Wait for their in-flight database
   transactions to finish.
6. Start exactly one master with `DATA_ENCRYPTION_ENABLE=true`. Do not start a
   replica or restore traffic until that master completes every protected-data
   migration and startup validation successfully.
7. Start the remaining nodes with `DATA_ENCRYPTION_ENABLE=true`, wait for each
   node to complete validation, and only then restore traffic.

The false-to-true cutover is deliberately writer-quiesced. It is not a rolling
restart: a still-running preparation-mode or old process can write plaintext
after another node has migrated the same row. Transactional row locking on
MySQL and PostgreSQL, and transaction write-conflict detection on SQLite,
prevent a concurrent update from being silently overwritten inside a
migration batch. Enforced runtime reads reject any plaintext reintroduced
afterward, but neither mechanism can make an old binary honor the new storage
format.

Do not treat `DATA_ENCRYPTION_ENABLE=false` as a steady-state mode. Switching
it back to `false` does not decrypt existing envelopes and does not make an old
binary compatible with a migrated database. Before the first envelope is
written, rollback to the old binary remains possible. After migration, an old
binary requires restoring a verified pre-migration database backup; toggling
the flag is not a ciphertext rollback.

### Gemini task-result provider URI cutover

Gemini video task-result provider URIs are server-only operational state in
the `tasks:provider_result_uri` encryption domain. Public task data and logs
contain only the local proxy projection. A non-empty value in this column is
always an envelope, including while `DATA_ENCRYPTION_ENABLE=false`; that flag
does not permit plaintext provider URIs. Before any Gemini task-result writer
runs, configure the complete keyring and active key identifier on every node.

Before cutover, verify that a pre-migration backup can be restored. Drain task
submission, polling, real-time fetch, and video-content traffic, then wait for
every in-flight task write to finish. After every old writer is gone, start
exactly one master. That master performs `Task` AutoMigrate, provider-result
envelope rewrap, task credential/private-data migrations, Gemini legacy
backfill, and startup validation in that order. Do not start replicas or
restore traffic before it succeeds. Start each remaining node with the same
keyring, require its startup validation to succeed, and only then resume
traffic. Run one final bounded, idempotent Gemini backfill and validation after
all old writers are confirmed gone. Execute that final pass by draining task
traffic again, waiting for in-flight writes to finish, and performing one
controlled restart of exactly one master while no other master is starting.
The pass is a no-op only when the
`Gemini task result privacy migration completed` log reports `updated=0`,
`private_captured=0`, and `private_cleared=0`, followed by a successful
`Gemini task result privacy validation completed` log. If any update count is
non-zero or validation fails, keep task traffic drained, investigate, and
repeat the controlled one-master restart until the measured pass is a no-op.

An old binary cannot preserve the encrypted provider-URI/public-projection
boundary during a mixed-version window. After the first encrypted
provider-result or public-projection write, a security-preserving rollback
must either restore the verified pre-migration backup or keep the new-reader
guards deployed. Under the guard-preserving alternative, every old reader and
writer must remain stopped and must not receive task submission, polling,
real-time fetch, or video-content traffic. Toggling
`DATA_ENCRYPTION_ENABLE` does not decrypt this column. Include
`tasks:provider_result_uri` in normal root-key rewrap and key-usage validation,
and retain required keys until that validation completes.

## Root-key rotation

Rotate without decrypting and re-encrypting payloads:

1. Add the new key to `DATA_ENCRYPTION_KEYS` on every node while retaining the
   current active key.
2. Restart or redeploy every node and verify that all nodes have the expanded
   keyring.
3. Change `DATA_ENCRYPTION_ACTIVE_KEY_ID` to the new identifier on every node
   while retaining both keys, but do not remove the old key.
4. Enter the same writer-quiesced maintenance window used for initial
   activation. Stop or drain every node using the old active identifier and
   wait for in-flight protected writes to finish.
5. Start exactly one master with the new active identifier. The master
   authenticates each payload and rewraps only its random data-encryption key
   under the new root key. Start replicas and restore traffic only after the
   master succeeds.
6. Keep the old root key configured until the post-migration
   `reversible secret key usage` logs for every protected domain no longer
   list the old identifier and every node completes validation with the
   expanded keyring.
7. Remove the old key from every node in one coordinated deployment.

Removing a key too early is startup-fatal for any database row that still
references it. Redis user-cache entries are bounded by the normal cache TTL;
an unreadable stale entry falls back to the validated database value and is
replaced under the active key.

## Failure behavior

Startup fails closed for a malformed envelope, authentication failure,
unknown key identifier, protected plaintext left while enforcement is enabled,
or an unavailable keyring required by existing data. Errors report only the
storage domain and row identifier; they do not include plaintext, ciphertext,
keys, or credential-derived prefixes.

Migrations scan bounded primary-key batches inside transactions. MySQL and
PostgreSQL lock the selected rows before transforming or updating them;
SQLite aborts a conflicting transaction instead of silently overwriting a
concurrent write. Identifier and envelope-marker compare-and-swap predicates
add another conflict check. Plaintext credentials are never used as SQL
predicates. Ordinary user settings that contain neither a webhook secret nor
a Gotify token remain plaintext JSON and continue to work on a keyless
deployment.
