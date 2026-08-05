# NEW API Ultra deployment

This is the authoritative deployment path for this publication repository. It
builds the checked-out source and does **not** silently pull
`calciumion/new-api:latest`. The original New API/QuantumNous project identity
and upstream references remain in the repository; this guide only describes
the publication snapshot.

## 1. Requirements

- Docker Engine/Desktop with Docker Compose v2
- A host that can run Linux containers (amd64 or arm64)
- `openssl` for generating deployment secrets
- Outbound network access during the first source build

The repository intentionally does not commit `web/dist`. The production
Dockerfile builds the frontend and embeds it into the Go binary, so a clone is
deployable after the image build without a generated frontend directory in Git.
The `tests` directories and `*_test.go` files are source-level regression tests;
they are useful for CI and local verification and are not copied into the final
runtime image.

The Compose files intentionally require a repository-local `.env`: it is used
both for Compose interpolation and as the complete runtime `env_file`, so
operator settings such as proxy trust, security flags, and frontend URLs are
not silently dropped. If an external secret manager is used, materialize an
equivalent mode-600 file or provide a deployment-specific Compose override;
passing only a different `--env-file` does not remove the local `env_file: .env`
requirement.

## 2. Build and start from source (recommended)

```bash
git clone https://github.com/guopengnaivoc/NEW-API-Ultra.git
cd NEW-API-Ultra
if [ ! -e .env ]; then ./scripts/bootstrap-env.sh; fi
docker compose config --quiet
docker compose up -d --build
docker compose ps
for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:3000/api/status; then
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    echo 'Application did not become ready within 60 seconds.' >&2
    exit 1
  fi
  sleep 2
done
```

Keep `--quiet` on the validation command: a bare `docker compose config`
renders the resolved passwords, session secrets, encryption keyring, and DSNs
to the terminal.

Open <http://127.0.0.1:3000> after the health check succeeds. The default
Compose file binds the application to loopback only and uses named volumes for
application data, logs, and PostgreSQL. This is intentionally a local-safe
default, not an Internet-facing production reverse proxy.

The bundled PostgreSQL and Redis services are the default targets. To point the
application at an existing database or Redis instance, set `SQL_DSN` and/or
`REDIS_CONN_STRING` in `.env`; the explicit values override the bundled-service
defaults, but the bundled services still start in the default file. For a
deployment that must not start local PostgreSQL or Redis, use the tested
external-only file instead:

```bash
# Set SQL_DSN and REDIS_CONN_STRING in .env first.
docker compose --env-file .env -f docker-compose.external.yml config --quiet
docker compose --env-file .env -f docker-compose.external.yml up -d --build
```

`docker-compose.external.yml` has no local database/cache services and does not
wait on them; the external endpoints, TLS, firewall, and backup policy remain
the operator's responsibility. It uses separate `new_api_external_*` volumes;
switching from the bundled Compose file starts with empty application data and
logs unless the operator performs an explicit, verified volume migration.

After the setup wizard, configure an upstream channel and model and send a
synthetic request. A page loading only proves that the web bundle and API
process are reachable; it does not prove that a provider, billing path, or
external database is configured correctly.

### Using a tagged prebuilt image

The GitHub Actions workflow publishes tagged images to GHCR. Only use a tag
that is visible on the repository's Releases/Packages page and verify its
digest:

```bash
export NEW_API_IMAGE=ghcr.io/guopengnaivoc/new-api-ultra:<tag>
if [ ! -e .env ]; then ./scripts/bootstrap-env.sh; fi
docker pull "$NEW_API_IMAGE"
docker buildx imagetools inspect "$NEW_API_IMAGE"  # or: docker manifest inspect -v "$NEW_API_IMAGE"
docker compose up -d --no-build
docker image inspect --format '{{join .RepoDigests "\n"}}' "$NEW_API_IMAGE"
```

The Compose file still contains the source `build` definition so a fresh clone
can build locally. `--no-build` is required when selecting a prebuilt image.
Do not rely on a mutable `latest` tag for an audited deployment.

For an immutable deployment, replace the tag with the multi-architecture digest
reported by `imagetools` (or `manifest inspect`) before starting Compose:

```bash
export NEW_API_IMAGE=ghcr.io/guopengnaivoc/new-api-ultra@sha256:<manifest-digest>
docker compose up -d --no-build
```

`docker image inspect` only reports what was pulled into the local engine; it is
not, by itself, proof that a registry tag is immutable. Keep the registry
manifest digest, source SHA, tag, and CI summary together as the release record.

Record the exact source SHA, image tag, and multi-architecture manifest digest
shown in the `Publish Docker image` Actions summary before distributing the
image. The tag alone is not sufficient provenance.

GHCR package visibility is an owner-level setting and is not changed by this
workflow. After the first tagged push, the repository owner must explicitly
choose whether `new-api-ultra` is public; anonymous users cannot pull a private
package. Verify an unauthenticated pull only after that setting is intentional.

The image workflow runs only for `vX.Y.Z`-style tags that exactly match the
committed `VERSION` file, and rejects a tag whose commit is not reachable from
the repository's default branch. Update `VERSION` in a source commit before
creating a new release tag; the workflow will not silently rewrite a mismatched
source version. It also requires an active repository ruleset matching
`refs/tags/v*` with deletion and non-fast-forward protection; configure and verify
that ruleset before creating a release tag so a published tag cannot silently
move to another digest. It also fails closed unless the repository variable
`ALLOW_GO_EPAY_REDISTRIBUTION=true`
has been set after written confirmation from the `go-epay` rights holder; leave it
unset while that legal review is open. A tag is not a security review or a claim
that unresolved application bugs have been fixed.

## 3. Secrets and encrypted channel credentials

`./scripts/bootstrap-env.sh` creates a mode-600 `.env` with:

- `POSTGRES_PASSWORD` and `REDIS_PASSWORD`
- `SESSION_SECRET` for access/refresh-session signing
- `CRYPTO_SECRET` for cache-key HMAC; keep it separate from the `DATA_ENCRYPTION_KEYS` keyring
- a 32-byte base64 `DATA_ENCRYPTION_KEYS` keyring and its active key id

The generator takes an atomic `.env.lock` directory lock and refuses to race a
second invocation. If a process is forcibly terminated while generating
secrets, inspect that no generator is still running before removing a stale
`.env.lock` directory and retrying.

The file is ignored by Git and Docker. Back it up securely, never commit it,
and do not paste it into issue reports or logs. Keep the effective session,
crypto, and data-encryption values stable across restarts and across all nodes
that share a database or Redis instance. Replacing them on an existing
installation can invalidate sessions or make previously encrypted channel
credentials unreadable.

The bootstrap script emits hex passwords so the default PostgreSQL DSN and
Redis URL do not contain URL-reserved characters. If you replace them with
hand-written passwords, use a DSN/URL-encoded value as required by the selected
database driver and test `docker compose config --quiet` before starting.

For public HTTPS, place Caddy, Nginx, Traefik, or an equivalent trusted reverse
proxy in front of the container and set all of the following in `.env`:

```dotenv
SESSION_COOKIE_SECURE=true
SESSION_COOKIE_TRUSTED_URL=https://example.com,https://admin.example.com
TRUSTED_PROXIES=<the proxy address or CIDR>
```

`SESSION_COOKIE_TRUSTED_URL` must contain exact HTTPS origins; it is not a relay
CORS allowlist and does not accept wildcard paths. Do not expose the default
HTTP port directly to the Internet.

The default application service drops Linux capabilities and enables
`no-new-privileges`, but it is a portable quick-start rather than a hardened
container sandbox: it does not set a read-only root filesystem, `tmpfs`, PID,
memory, or CPU limits. Add and test those controls in a deployment-specific
override, together with host firewalling and log/backup capacity limits, before
using the stack as an Internet-facing production service.

## 4. Data, logs, and upgrades

The default named volumes are normally prefixed by the Compose project name:

- `new-api-ultra_new_api_data`
- `new-api-ultra_new_api_logs`
- `new-api-ultra_pg_data`
- `new-api-ultra_new_api_external_data` and
  `new-api-ultra_new_api_external_logs` (external-service Compose file)

The exact names can be listed with `docker volume ls`. Back up PostgreSQL and
application data before upgrades. Do **not** run `docker compose down -v`
unless deleting all data is intentional. Keep `.env` together with the backup
but protect it separately because it contains live secrets.

The active PostgreSQL and Redis images, plus the commented MySQL and ClickHouse
examples, use exact version tags and multi-architecture OCI index digests
verified for amd64 and arm64 during this publication pass. A digest is not a
permanent security guarantee: re-check upstream support, release notes, target
architectures, licenses, and image vulnerability scans before changing or
promoting any of them. Never replace them with a floating `latest` tag.

## 5. Frontend development

```bash
if [ ! -e .env ]; then ./scripts/bootstrap-env.sh; fi
docker compose -f docker-compose.dev.yml up -d --build
cd web
bun install --frozen-lockfile
bun run dev -- --host 127.0.0.1 --port 5173
```

The development server is explicitly bound to <http://127.0.0.1:5173> above
and proxies API requests to the backend. The repository's `make dev-web`
shortcut deliberately binds `0.0.0.0` for LAN testing; use the explicit
loopback command instead on an untrusted network. The development Compose file
uses a placeholder embedded page; it is not the production frontend image.

## 6. Local verification and CI

The publication CI runs on `main` pushes and pull requests. The corresponding
local checks are:

```bash
cd NEW-API-Ultra
if [ ! -e .env ]; then ./scripts/bootstrap-env.sh; fi
(cd web && bun install --frozen-lockfile && DISABLE_ESLINT_PLUGIN='true' bun run build)
GOWORK=off go vet ./...
GOWORK=off go build ./...
GOWORK=off go test ./...
(cd relaykit && GOWORK=off go vet ./... && GOWORK=off go build ./... && GOWORK=off go test ./...)
(cd web && bun run typecheck && bun test)
docker compose config --quiet
```

For this pinned baseline, root `go vet` reports the known
`relay/channel/dify/adaptor.go:111:2: unreachable code` diagnostic. The
publication CI records it as a warning only when it is the exact sole
diagnostic; any additional or different diagnostic fails CI. This exception is
documented in [`PUBLICATION_VERIFICATION.md`](./PUBLICATION_VERIFICATION.md)
and is not a claim that the application is vet-clean.

The frontend build in the first command creates the otherwise-ignored
`web/dist` directory required by Go's `//go:embed` directive. The frontend and
Docker build produce ignored/generated files locally; they do not need to be
added to Git. If only backend compilation is needed, create a temporary
`web/dist/index.html` placeholder instead of claiming that the production UI
was built.

## 7. License, provenance, and redistribution boundary

This snapshot is an AGPLv3 derivative distribution. Keep `LICENSE`, `NOTICE`,
`THIRD-PARTY-LICENSES.md`, the New API/QuantumNous attribution, and the visible
upstream link when redistributing it. Read [`PROVENANCE.md`](./PROVENANCE.md)
and [`PUBLICATION_EXCLUSIONS.md`](./PUBLICATION_EXCLUSIONS.md) before making a
release.
The command-level evidence and known verification limits for this snapshot are
recorded in [`PUBLICATION_VERIFICATION.md`](./PUBLICATION_VERIFICATION.md).

`THIRD-PARTY-LICENSES.md` records that the GitHub metadata for
`github.com/Calcium-Ion/go-epay v0.0.4` says MIT, while the exact v0.0.4 tag
contains no license file. Obtain and record written confirmation from the rights
holder before advertising a public binary or hosted service. This repository
does not provide legal clearance and does not claim that every outstanding
upstream or local issue is resolved.

Compose service images are separately distributed software and are not
relicensed by this repository. In particular, the official Redis image states
that Redis 7.4.x is offered under the RSALv2 or SSPLv1 terms; review the
[Redis license notice](https://redis.io/legal/licenses/) and the [official
Redis image documentation](https://hub.docker.com/_/redis), as well as the
PostgreSQL/MySQL/ClickHouse image terms for the exact versions you deploy.
