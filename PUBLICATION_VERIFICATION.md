# Publication verification record

This record describes the checks run against the `NEW API Ultra` publication
staging tree. It is evidence for this snapshot, not a claim that the
application has no remaining defects.

Last updated: `2026-08-05T20:09:02Z` (verification commands were run in the
same staging tree; generated build output was removed before publication).

## Pinned scope

- Application baseline: local committed `main` snapshot
  `49270e59f475761b457879693e0c3ebd71329fac` (parents
  `05d5c10e4ab2418a0f421190c0d3a81fb8e91dc2`,
  `33fd5a0ff326673908ef08a9452bba856d3b32a1`; not currently resolvable as a
  public `QuantumNous/new-api` commit)
- Snapshot label: `v0.1.0-main.49270e59`
- Intended repository: <https://github.com/guopengnaivoc/NEW-API-Ultra>
- Remote CI evidence commit: `ee99409cb6c99773eec3ec5bbd036062bcb5dbde`;
  the publication-only docs/CI patch now being recorded does not change
  application behavior and requires its own CI run before tagging.
- Generated `web/dist`, package-manager directories, `.env`, databases, and
  logs are intentionally absent from the source publication.
- No Go, TypeScript/TSX, relay, controller, model, migration, or other
  application/business file was intentionally changed during publication.

## Passing checks

The following checks passed in the staging tree (fresh output should be
re-run after any source or dependency change):

- `cd web && bun install --frozen-lockfile`
- Frontend production build with the snapshot `VERSION` (`bun run build`,
  `DISABLE_ESLINT_PLUGIN=true`)
- Frontend `bun run typecheck`
- Frontend `bun run format:check`
- Local frontend `bun test` — 418 passed, 0 failed across 75 files
- Root `GOWORK=off go build ./...`
- Root `GOWORK=off go test ./...`
- Independent `relaykit` `go vet`, `go build`, and `go test`
- `docker compose config --quiet` for the bundled, development, and
  external-service Compose files with synthetic non-secret values
- Bootstrap script syntax, mode-600 output, keyring shape, refusal to
  overwrite an existing `.env`, and bounded concurrent-invocation locking; a
  fault-injected OpenSSL failure also leaves no partial `.env` and is covered by
  the CI validation step
- YAML parsing for Compose and GitHub workflow files; shell syntax for the
  bootstrap and publication workflow scripts
- Markdown relative-link audit — 0 missing links
- Baseline parity audit against `49270e59f475761b457879693e0c3ebd71329fac`:
  2,329 baseline files, 2,183 publication files, 25 publication-only
  modifications, 7 publication additions, and 153 intentionally excluded
  local/upstream-only files; no Go, TypeScript/TSX, relay, controller, model,
  migration, or other business-source drift detected

## Known non-passing or unverified boundaries

- The last completed GitHub Actions CI run
  [31040796003](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31040796003)
  for evidence commit `ee99409cb6c99773eec3ec5bbd036062bcb5dbde` completed
  with `failure`: the
  backend job and Docker build smoke test passed, but the frontend test job
  reproduced one existing failure (`417 pass`, `1 fail` across 418 tests). The
  failing test is `web/src/features/dashboard/components/models/__tests__/chart-theme-recovery.test.tsx:131`,
  where `applicationAttempts` was `0` instead of `1` in “a dashboard chart
  recovers in place after a transient theme failure”. Local execution passed
  418/0, so this publication does not treat CI as green; no business source or
  test was changed, excluded, or serialized to hide the failure. The release
  workflow consequently remains fail-closed until a fresh tagged-commit CI run
  passes all required jobs.

- Root `GOWORK=off go vet ./...` reports the pre-existing unreachable return at
  `relay/channel/dify/adaptor.go:111` in the pinned baseline. It was not
  removed because this publication pass does not modify remaining business
  bugs. The CI step has a deliberately exact, fail-closed allowlist for this
  one pinned diagnostic: it emits a warning and a job-summary entry, while any
  additional or different vet diagnostic still fails the job. This is not a
  claim that the baseline is vet-clean.
- `web` lint reports existing source diagnostics (the fresh run returned 348
  error diagnostics and 73 warnings), and `copyright:check` reports two
  existing headers (`src/features/channels/lib/channel-field-update.ts` and
  `src/lib/external-url.ts`). `format:check` passes. These source issues were
  not changed or hidden by publication configuration.
- A clean source checkout cannot run the Go `//go:embed web/dist` package
  until the frontend is built (or CI creates its documented placeholder). The
  frontend build above passed; the root Go build/test evidence was collected
  with the generated `web/dist` present, and that directory is excluded from
  the publication tree.
- A local production Docker build was not completed because the Docker Hub
  registry timed out while resolving the pinned base image; the local Docker
  installation also lacks the Buildx plugin. GitHub CI performs the pinned
  amd64 smoke build, while the release workflow performs the multi-architecture
  build. Local arm64 and a full container runtime/healthcheck smoke were not
  independently completed.
- A fresh local `go mod download` attempt was blocked by a timeout talking to
  `proxy.golang.org`; the Go build/test results above used the already cached
  module set. GitHub CI has explicit root and `relaykit` module-download steps
  so a cache miss is visible rather than being hidden inside vet/build output.
- No `govulncheck`, transitive SBOM/CVE scan, registry image scan, live
  provider/database/Redis matrix, or public HTTPS reverse-proxy test was run
  in this staging pass.

## Redistribution boundary

`THIRD-PARTY-LICENSES.md` records an unresolved license boundary for
`github.com/Calcium-Ion/go-epay v0.0.4`: GitHub repository metadata reports MIT,
but the exact v0.0.4 tag has no `LICENSE`, `COPYING`, or `NOTICE` file. This
publication does not convert that conflicting evidence into legal clearance.
Obtain and record written confirmation from the rights holder before
redistributing a binary or operating a hosted service. Preserve `LICENSE`,
`NOTICE`, the upstream attribution, and all third-party notices.

The GHCR workflow therefore fails closed until the repository owner sets
`ALLOW_GO_EPAY_REDISTRIBUTION=true` after recording that confirmation. The gate
does not itself grant rights to source, module, CI-log, or BuildKit-cache artifacts;
those retention and access surfaces require the same rights-holder review.
