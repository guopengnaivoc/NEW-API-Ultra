# Token cache rolling upgrade

This procedure applies when upgrading from a release that stores complete
relay-token metadata under `token:<sha256>` to a release that stores only the
database lookup hint under `token:v2:<sha256>`.

The database remains authoritative for every relay-token authentication
decision. Current binaries read and write only the `token:v2:` namespace.
Their cache writes atomically clear the matching legacy key before publishing
the v2 lookup hint, and token mutation, invalidation, deletion, and rotation
remove both namespaces.

## Required rollout sequence

1. Identify every application node connected to the same primary database and
   Redis deployment.
2. Drain old nodes from request traffic before relying on the authorization
   repair. A mixed-version deployment is operationally compatible, but an old
   node can still read or republish stale authorization metadata in the legacy
   namespace.
3. Replace all old binaries. Do not leave an old node running as a long-lived
   rollback target.
4. After the last old process has stopped, purge only legacy relay-token cache
   keys. This is required when `SYNC_FREQUENCY` is non-positive because those
   keys do not expire, and is recommended for every upgrade so an accidentally
   restarted old process cannot reuse a stale entry.
5. Verify that normal relay authentication succeeds and that no old node is
   recreating legacy keys.

Do not delete `token:v2:*`. Also do not pass `token:*` directly to `DEL` or
`UNLINK`, because that pattern includes the current namespace. First inventory
the exact legacy shape and inspect the resulting file:

```bash
redis-cli --scan --pattern 'token:*' \
  | grep -E '^token:[0-9a-f]{64}$' \
  > /tmp/new-api-legacy-token-cache-keys.txt

wc -l /tmp/new-api-legacy-token-cache-keys.txt
sed -n '1,20p' /tmp/new-api-legacy-token-cache-keys.txt
```

After verifying the target Redis endpoint and the sampled keys, remove only
the inventoried legacy entries:

```bash
while IFS= read -r key; do
  redis-cli UNLINK "$key"
done < /tmp/new-api-legacy-token-cache-keys.txt
```

Supply the normal TLS, authentication, database-selection, or cluster options
to each `redis-cli` invocation. For a clustered Redis deployment, perform the
inventory and purge against every relevant primary shard.

## Mixed-version and rollback limitation

Current writers never publish the v2 ID-only schema into the legacy namespace,
so an old reader sees a legacy miss and falls back to the database instead of
consuming incompatible metadata. Current readers ignore legacy entries
entirely, so stale full metadata cannot influence their authorization result.

Namespace isolation cannot repair an old process: while any old node remains
active, it can recreate a legacy full-metadata hash and another old node can
consume it. Rollback to an affected binary therefore reopens the original
authorization risk. If rollback is unavoidable, drain traffic, complete the
rollback as a coordinated deployment, and treat the deployment as affected
until a fixed version is restored and the targeted legacy purge is repeated.
