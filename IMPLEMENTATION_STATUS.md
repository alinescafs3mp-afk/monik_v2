# V21: clean TV presentation and access correctness, PRE-RELEASE

Base `3303e991a2624c945662355e22fa7cfb8ef06a6a`. Read `docs/AUDIT_REVIEW_21_2026-09-16.md`, `docs/V21_CLEAN_TV_RU.md` and `docs/audit/validation-review21-2026-09-16.json`. Prior host V20 status/ledger are preserved under docs/history/*_THROUGH_V20.md.

Implemented: device-local clean TV with top recovery/diagnostics/paging, continued shared width/density synchronization, square 30px console/details links, viewer self-logout without general mutation rights, delayed history-export reauthorization, explicit export storage errors, and transactional still-current credential successor registration. No agent protocol/database migration or agent authority change.

Host executed `scripts/verify-audit21.sh --with-browser`: Go 1.27.1 race 478/714/27, 199 frontend tests, native processes 3/3 uid=1000, browser **50/50**. Auditor browser was BLOCKED_BEFORE_LOGIN; five new scenarios ran here. Vue omitted-Boolean `interactive` is default true so ordinary overview handles remain. UID isolation fixture NOT RUN. This is not a claim of completed v3 or a completed external security audit.

No agents must be replaced for V21. Preserve current identifiers, credentials, trust, history, shared TV profile and local console opt-in. `operation.retry_selected` and independent service-host self-update remain fail-closed. Full restore, physical TV, native boot, perimeter and fleet/soak gates stay open in `docs/RELEASE_COMPLETION_PLAN.md`.
