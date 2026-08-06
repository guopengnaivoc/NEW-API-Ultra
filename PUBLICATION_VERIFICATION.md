# Publication verification record

This record describes the checks run against the `NEW API Ultra` publication
staging tree. It is evidence for this snapshot, not a claim that the
application has no remaining defects.

Last updated: `2026-08-06T03:27:12Z` (verification commands were run in the
same staging tree; generated build output was removed before publication).

## Pinned scope

- Application baseline: local committed `main` snapshot
  `49270e59f475761b457879693e0c3ebd71329fac` (parents
  `05d5c10e4ab2418a0f421190c0d3a81fb8e91dc2`,
  `33fd5a0ff326673908ef08a9452bba856d3b32a1`; not currently resolvable as a
  public `QuantumNous/new-api` commit)
- Snapshot label: `v0.1.0-main.49270e59`
- Intended repository: <https://github.com/guopengnaivoc/NEW-API-Ultra>
- The historical default-run CI evidence below is pinned to commit
  `ee99409cb6c99773eec3ec5bbd036062bcb5dbde`. The P1 remediation candidate is
  `eeda656a9ef85f33ffea35705540f9933a529269`; it changes only the frontend CI
  runner invocation and still requires a fresh exact-main run after merge and
  before tagging.
- Generated `web/dist`, package-manager directories, `.env`, databases, and
  logs are intentionally absent from the source publication.
- No Go, TypeScript/TSX, relay, controller, model, migration, or other
  application/business behavior was intentionally changed during publication;
  the sole Go source change is the comment-only credential-example
  sanitization recorded above.

## Passing checks

The following checks passed in the staging tree (fresh output should be
re-run after any source or dependency change):

- `cd web && bun install --frozen-lockfile`
- Frontend production build with the snapshot `VERSION` (`bun run build`,
  `DISABLE_ESLINT_PLUGIN=true`)
- Frontend `bun run typecheck`
- Frontend `bun run format:check`
- Local frontend `bun test --isolate` — 418 passed, 0 failed across 75 files
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
- Secret-pattern scan found no live credential after replacing the
  credential-shaped masking-comment example in
  `relaykit/relayconvert/kitutil/mask.go`; the remaining PEM markers are parser
  delimiters. GitHub secret-scanning alert #1 was reviewed and resolved as a
  false positive with an audit comment.
- Baseline parity audit against `49270e59f475761b457879693e0c3ebd71329fac`:
  2,329 baseline files, 2,183 publication files, 26 publication-only
  modifications (including the one comment-only source-note change above), 7
  publication additions, and 153 intentionally excluded local/upstream-only
  files; no runtime Go, TypeScript/TSX, relay, controller, model, migration, or
  other business-source drift detected
- Candidate PR CI [31068276213](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31068276213)
  attempt 2 passed all three required jobs on the Linux runner after the
  `bun test --isolate` change: frontend typecheck/build/test, backend
  vet/build/test, and Docker build smoke test.

## Known non-passing or unverified boundaries

- A historical GitHub Actions CI evidence run
  [31040796003](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31040796003)
  for evidence commit `ee99409cb6c99773eec3ec5bbd036062bcb5dbde` completed
  with `failure`: the
  backend job and Docker build smoke test passed, but the frontend test job
  reproduced one existing failure (`417 pass`, `1 fail` across 418 tests). The
  failing test is `web/src/features/dashboard/components/models/__tests__/chart-theme-recovery.test.tsx:131`,
  where `applicationAttempts` was `0` instead of `1` in “a dashboard chart
  recovers in place after a transient theme failure”. Local execution passed
  418/0. The candidate remediation changes only the runner isolation mode; no
  business source or test was changed, excluded, or serialized to hide the
  failure. The candidate's PR CI passed, but the release workflow still
  requires a fresh exact tagged-commit CI run after the change is merged.

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