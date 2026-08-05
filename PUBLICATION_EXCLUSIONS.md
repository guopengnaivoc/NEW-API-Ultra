# Publication exclusions

The public snapshot intentionally omits local-only material that is not required to build or run the application:

- IDE and operating-system metadata (`.idea`, `.DS_Store`)
- AI-agent collaboration state and internal implementation reports
- Local audit plans and session logs
- `.env`, databases, logs, dependency directories, and generated `web/dist` output
- Upstream-only publishing, Electron, GitCode-sync, and anti-slop workflow files that
  referenced credentials or namespaces not owned by this publication repository

The removed upstream workflow/script paths are:

- `.github/workflows/docker-build.yml`
- `.github/workflows/docker-image-branch.yml`
- `.github/workflows/electron-build.yml`
- `.github/workflows/pr-check.yml`
- `.github/workflows/release.yml`
- `.github/workflows/sync-release-to-gitcode.yml`
- `.github/scripts/release-workflow-security-test.sh`
- `.github/scripts/resolve-release-tag.sh`

The application source, tests, public documentation, migrations, Docker build files,
the repository CI, the GHCR publication workflow, licenses, notices, and upstream
attribution remain included. The exclusions apply only to this publication snapshot;
the original checkout and its historical records are unchanged.

The root `AGENTS.md`, `CLAUDE.md`, and `web/AGENTS.md` remain intentionally
included as project development/contribution conventions. They are not treated
as private audit reports or credentials; local agent state under `.agents/` and
`.superpowers/` is excluded.
