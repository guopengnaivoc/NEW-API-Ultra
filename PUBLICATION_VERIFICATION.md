# Publication verification record

This record describes the checks run against the `NEW API Ultra` publication
staging tree. It is evidence for this snapshot, not a claim that the
application has no remaining defects.

Last updated: `2026-08-06T14:23:36Z` (the corrected release, workflow logs, and GHCR
registry manifests were re-verified; generated build output remains excluded from
the source publication).

## Pinned scope

- Application baseline: local committed `main` snapshot
  `49270e59f475761b457879693e0c3ebd71329fac` (parents
  `05d5c10e4ab2418a0f421190c0d3a81fb8e91dc2`,
  `33fd5a0ff326673908ef08a9452bba856d3b32a1`; not currently resolvable as a
  public `QuantumNous/new-api` commit)
- Recommended snapshot label: `v0.1.0-main.49270e59-r1`
- Intended repository: <https://github.com/guopengnaivoc/NEW-API-Ultra>
- Corrected publication/tag commit:
  `add3b51627ba4d6dd608a9d9f3512318778221de`; immutable tag
  `v0.1.0-main.49270e59-r1` points to this commit. The application baseline
  remains the local source snapshot above; publication-only commits do not
  silently change business behavior.
- Historical first publication/tag commit:
  `297e84d127a372cd91d57532bf3038a2b2805d00`; immutable tag
  `v0.1.0-main.49270e59` remains at that commit and was not rewritten.
- The P1 frontend-CI remediation is merged as
  `0654c55965cf70a861089c041c7141b01be5765a`; the CI-only release-gate fix is
  merged as `9b6eb7ff05504e4f63d2c77dcc4ffcefa83379d7`; the P2 OCI-level/prefix fix is
  merged through PR #6 as `a1b0e4e718312ed5624aac8dee83203b70fd81ae`;
  and the format-only VERSION newline follow-up is merged through PR #7 as the
  final tagged commit `add3b51627ba4d6dd608a9d9f3512318778221de`.
- Exact push/main CI run `31106875917` passed all three required jobs for the
  corrected tagged commit.
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
- Baseline parity audit at the first tagged publication commit against
  `49270e59f475761b457879693e0c3ebd71329fac`: 2,329 baseline files, 2,183
  publication files, 26 publication-only
  modifications (including the one comment-only source-note change above), 7
  publication additions, and 153 intentionally excluded local/upstream-only
  files; no runtime Go, TypeScript/TSX, relay, controller, model, migration, or
  other business-source drift detected. Later commits through the corrected tag
  change only CI/release configuration, VERSION metadata, and publication docs.
- Candidate PR CI [31068276213](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31068276213)
  attempt 2 passed all three required jobs on the Linux runner after the
  `bun test --isolate` change: frontend typecheck/build/test, backend
  vet/build/test, and Docker build smoke test.
- OCI annotation PR [#6](https://github.com/guopengnaivoc/NEW-API-Ultra/pull/6)
  CI [31105788420](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31105788420)
  and VERSION-newline PR [#7](https://github.com/guopengnaivoc/NEW-API-Ultra/pull/7)
  CI [31106610550](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31106610550)
  each passed the same three required jobs. Final exact push/main CI
  [31106875917](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31106875917)
  also passed all three jobs for the corrected tagged commit.

## Release and image evidence

- Recommended corrected release:
  [v0.1.0-main.49270e59-r1](https://github.com/guopengnaivoc/NEW-API-Ultra/releases/tag/v0.1.0-main.49270e59-r1),
  immutable commit `add3b51627ba4d6dd608a9d9f3512318778221de`.
- Exact push/main CI for that commit:
  [31106875917](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31106875917)
  — backend, frontend, and Docker smoke jobs passed.
- Tag-triggered publication:
  [31107211995](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31107211995)
  — tag/ruleset, redistribution authorization, exact-CI, generated-annotation,
  and multi-architecture build/push gates all passed.
- Published references:
  `ghcr.io/guopengnaivoc/new-api-ultra:v0.1.0-main.49270e59-r1` and
  `ghcr.io/guopengnaivoc/new-api-ultra:sha-add3b51`. Both resolve to OCI index
  digest `sha256:a31091ca37f2f94164b3a098526bd8ef04d60dce38dc9dd11263677ebdf50149`.
- Anonymous GHCR token exchange and manifest retrieval returned the OCI index,
  two Linux image manifests, and two SBOM/provenance attestation manifests. The
  index-level `org.opencontainers.image.revision` equals full commit
  `add3b51627ba4d6dd608a9d9f3512318778221de`. Fetching the platform manifest
  bodies by digest verified the same standard key/value on `linux/amd64`
  (`sha256:a3045df2ec04f483335d3d9eddf2f8528f984ef71ab92493855c060dfdb9cb2b`)
  and `linux/arm64`
  (`sha256:bbb4f073380d9a76874a968261f50f12ad695beb543ad71c3f2e0021d0635ee1`).
  Both platform config labels contain the same revision. No malformed
  `manifest:org.opencontainers.image.revision` key was present in those
  manifest-body annotation maps.
- Historical first release `v0.1.0-main.49270e59` remains unchanged at commit
  `297e84d127a372cd91d57532bf3038a2b2805d00` and digest
  `sha256:9ccc1d3aea6b687a713e4cb167b4178a6236854f8734c7e91a6bafd8d3653aa8`.
  Its image config labels and SLSA provenance identify commit `297e84d`, but
  registry inspection confirms that its OCI index and both platform manifest
  bodies have no revision annotation. The tag and image were not moved,
  deleted, or overwritten; use the `-r1` release for corrected metadata.
- Historical workflow evidence is retained: tag run
  [31093075624](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31093075624)
  stopped before login/build because REST ruleset fields were redacted to the
  read-only Actions token, and recovery dispatch
  [31095075366](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31095075366)
  published the first immutable artifact without moving its tag. PR #3 moved the
  ruleset check to fail-closed GraphQL, while PR #6 fixed metadata-action level
  handling and guards against double-prefixed output.

## Known non-passing or unverified boundaries

- Historical CI run
  [31040796003](https://github.com/guopengnaivoc/NEW-API-Ultra/actions/runs/31040796003)
  for evidence commit `ee99409cb6c99773eec3ec5bbd036062bcb5dbde` remains a
  recorded failure: the backend and Docker smoke jobs passed, while the
  frontend test job reproduced one existing failure (`417 pass`, `1 fail`).
  That run is not used as release evidence. The P1 change altered only the
  frontend test runner isolation; no business source or test was changed to
  hide the failure. The fresh exact-main run for the immutable tagged commit and the post-merge
  main runs for the original and corrected publication commits passed all
  three required jobs.

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

## Redistribution authorization

`THIRD-PARTY-LICENSES.md` records that GitHub repository metadata reports MIT,
but the exact `github.com/Calcium-Ion/go-epay v0.0.4` tag has no `LICENSE`,
`COPYING`, or `NOTICE` file. The repository owner has confirmed receipt of
written authorization from the `go-epay` rights holder for the intended
redistribution of this resolved dependency. The original authorization is
retained privately and is not reproduced here; this is an operator attestation,
not an independent legal opinion. Preserve `LICENSE`, `NOTICE`, the upstream
attribution, and all third-party notices, and keep distribution within the
authorization's scope.

The GHCR workflow may proceed only after the repository owner sets
`ALLOW_GO_EPAY_REDISTRIBUTION=true` after retaining that confirmation. The gate
does not itself grant rights to source, module, CI-log, or BuildKit-cache artifacts;
those retention and access surfaces require the same rights-holder review.