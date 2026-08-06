# Publication provenance

This public snapshot is a derivative distribution of [QuantumNous/new-api](https://github.com/QuantumNous/new-api).

- Source lineage: local checkout of `https://github.com/QuantumNous/new-api.git`
- Source checkout ref: local `main`
- Source commit: `49270e59f475761b457879693e0c3ebd71329fac`
- Source parents: `05d5c10e4ab2418a0f421190c0d3a81fb8e91dc2`,
  `33fd5a0ff326673908ef08a9452bba856d3b32a1`
- Source tree object: `4e4afc4d30f803016658a544e0407524f3ba2fdf`
- Recommended snapshot label: `v0.1.0-main.49270e59-r1`
- Publication repository: `https://github.com/guopengnaivoc/NEW-API-Ultra`
- Corrected immutable release/tag commit: `add3b51627ba4d6dd608a9d9f3512318778221de`
- Exact tagged-commit CI run: `31106875917` (all three required jobs passed)
- GHCR image: `ghcr.io/guopengnaivoc/new-api-ultra:v0.1.0-main.49270e59-r1`
- Multi-architecture OCI index digest: `sha256:a31091ca37f2f94164b3a098526bd8ef04d60dce38dc9dd11263677ebdf50149`
- Historical first tag: `v0.1.0-main.49270e59` at commit
  `297e84d127a372cd91d57532bf3038a2b2805d00`, digest
  `sha256:9ccc1d3aea6b687a713e4cb167b4178a6236854f8734c7e91a6bafd8d3653aa8`

The source commit above is a local merge snapshot, not an upstream public commit
identifier: as of 2026-08-05 the GitHub API did not resolve that SHA in
`QuantumNous/new-api`. The upstream URL is retained for lineage, attribution, and
cross-checking; it is not a claim that this exact SHA can be fetched from the
upstream repository. The application-code baseline is exported from the exact
committed local tree identified above. This publication repository then applies
only the packaging changes listed below; it does not silently incorporate another
branch, worktree, stash, or unreviewed business patch. Generated frontend output
(`web/dist`) and dependency directories are intentionally omitted; the production
Dockerfile builds the frontend before compiling the Go binary. The original
license, NOTICE, third-party notices, project identity, and upstream link are
preserved.

Publication-only changes are limited to Docker/Compose defaults (including the
external-service profile), the bootstrap script, CI and GHCR workflow, version
metadata, public deployment documentation, issue/PR and security/community
templates, dependency/license inventory, and provenance/exclusion/source-notice
notes. No remaining application/business bug was intentionally changed during
this packaging or annotation-correction pass.

The publication CI keeps the pinned baseline `go vet` diagnostic at
`relay/channel/dify/adaptor.go:111` visible as a warning with an exact,
fail-closed allowlist; any additional or different diagnostic fails CI. This
packaging decision does not make the application baseline vet-clean.

The first immutable image publication was recovered through workflow dispatch
run `31095075366` after tag run `31093075624` failed before the build because the
read-only Actions token could not inspect redacted REST ruleset fields. The
dispatch used the existing tag and selected commit; it did not move or recreate
the tag. Its metadata-action step generated a dispatcher-SHA manifest annotation,
but the build step did not pass annotations to Buildx. Registry inspection
therefore found no `org.opencontainers.image.revision` annotation on the
historical image's OCI index or either platform manifest body. Its config labels
and SLSA provenance still identify selected commit `297e84d`; the historical tag
and digest remain unchanged as evidence.

The annotation-corrected release `v0.1.0-main.49270e59-r1` was built from commit
`add3b51627ba4d6dd608a9d9f3512318778221de` after exact push/main CI run
`31106875917` passed. Publication run `31107211995` used detached Git metadata,
a plain OCI annotation key, metadata levels `manifest,index`, and a pre-build
assertion against missing or double-prefixed revision output. Anonymous registry
inspection verified the same full revision SHA on the OCI index, the fetched
`linux/amd64` and `linux/arm64` manifest bodies, and both image config labels. The
version tag and `sha-add3b51` resolve to the same index digest recorded above.

A release is authoritative only when the tag, source commit, CI result, GHCR
digest, and registry metadata are recorded together. This provenance record does
not claim that every outstanding issue in the upstream or local issue backlog is
resolved. Verify the tagged source and deployment checks before using it for a
public service.
