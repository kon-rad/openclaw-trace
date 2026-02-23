# Phase 6: Hardening - Context

**Gathered:** 2026-02-24
**Status:** Ready for planning/execution

<domain>
## Phase Boundary

Finalize release-readiness guardrails:
- binary size budget enforcement (<15MB linux/amd64 stripped)
- resource measurement hooks (RSS probe)
- integration test covering ingest-to-push path with a real SQLite file
- release packaging config for multi-arch binaries via GoReleaser

</domain>

<decisions>
## Locked Decisions

- Keep CGO disabled for release builds.
- Add CI gate for binary size budget.
- Keep integration test resilient in restricted sandboxes (skip when network bind unavailable).
- Include GoReleaser config for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.

</decisions>

---

*Phase: 06-hardening*
*Context gathered: 2026-02-24*
