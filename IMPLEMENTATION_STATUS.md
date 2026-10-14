# V22: Spark light, source and deployment status (2026-10-15)

Base `dc6511eeeb2df9b85b5be59a1b6074648f0cdc08`. The current source provides a separate Linux ARM64 light collector, negotiated schema-3 telemetry, one identity across global/Spark views, typed local control, bounded owner logs and flavor-aware installation/recovery. Read the [operator guide](docs/V22_SPARK_LIGHT_RU.md) and [acceptance ledger](ACCEPTANCE_LEDGER.md).

## Implemented source

Seven fixed profiles share the pre-existing Python Coordinator, StateStore, collection lock and memory reservations: five original V4 profiles plus fixed legacy Qwen/GPT adapters. The five-profile V4 manifest remains unchanged. Root loads pinned code through sealed descriptors; preserved script/TUI names delegate to the same coordinator. Monik introduces no second inference owner. See [reusable source and installation inputs](tools/spark-controller/README.md). Catalog-only fallback still exists for unsupported profiles; it is no longer the description of these seven integrations.

The typed bridge binds authority, revision, run/attempt and immutable physical identity. Restart requires independently authorized stop/start stages, verified release and no-victim preflight. Model configuration and generation budgets remain owned by the existing launcher/client. Ambiguous observations and releases fail explicitly. Completed operation retention validates whole receipts, retains malformed/unresolved evidence and preserves bounded Bloom/deadline replay guards.

Fixed per-profile log sources use bounded upstream/viewer counts and buffers. Pinned protected-value redaction is revalidated at actual read/emission boundaries; raw model logs do not enter ordinary SQLite history. Report-only negotiated gzip keeps the exact JSON/receipt identity, authenticated same-sequence committed-ACK requirements and independent encoded/decoded caps. Owner/viewer authority, ingest fencing, installer recovery and explicit unknown historical spool loss remain enforced.

## Evidence and running installation

| Layer | Current disposition |
|---|---|
| Source/offline | Go race/vet/module verification PASS; UI 215 tests/types/build PASS; all three installer templates and AMD64/ARM64 builds PASS. Final copied Python suite: 342 PASS in 14.141 s, including systemd template metadata and typed D-Bus empty-command guards. Actual Python-produced frames pass the strict Go consumer offline; scoped independent reviews PASS. |
| Build identity | Go/UI release `release-final7-20261015T0012` and later Python corrections have separate pins in the ledger. The Go/UI build source ID is not an identity for every later repository byte. |
| Deployment | F4 controller and both Spark monitors have been deployed. The three existing enrollment/trust identities are retained; seven profiles use the same coordinator. Seven-profile native observations, three harmless no-op actions and fixed Ling log streaming passed; engine cold-start/restart acceptance remains NOT_RUN. The idle resource measurement exposed repeated helper overhead; a batching correction is being validated, not yet accepted. |
| Preserved runtime | Ling was READY on the same pre-existing instance at the supplied checkpoint. Qwen owner STOP and GPT/MiniMax stop intent were preserved. The Monik upgrade performed no model generation or cold start; this does not establish seven-engine lifecycle acceptance. |
| F4 final native/browser | **PENDING**: installed pins/policy, typed no-effect/read-only backend observations, served desktop/mobile/TV and runtime preservation evidence must be entered in the ledger by the integrator. |
| F4 resources | **PENDING**: incremental collector+broker/helper CPU/PSS and measured wire traffic on the installed F4. Historical F3 numbers are retained separately in the ledger. |
| Model lifecycle | **NOT_RUN** for the final seven-profile acceptance unless an explicit maintenance observation is recorded. Source controls and passive readiness do not prove real start/stop/restart, cold start, inference latency or full-context fitting. |

Historical F3 evidence includes 18 native canary controls and 15 browser screens; those use a different artifact and do not close F4 acceptance. Log-stress and inference/cold-start tests remain NOT_RUN unless separately observed. CPU below 0.5% of one core is an engineering target, not an automatic release blocker. Original damaged databases/state, credentials, raw owner logs, machine identifiers and private handoff evidence are excluded from publication.

Source, deployment and runtime acceptance are separate. Physical TV, external perimeter, fleet/soak and full restore gates remain open where not exercised; this status does not declare a full-product release.

## Historical V21: clean TV presentation and access correctness

Base `3303e991a2624c945662355e22fa7cfb8ef06a6a`. Read `docs/AUDIT_REVIEW_21_2026-09-16.md`, `docs/V21_CLEAN_TV_RU.md` and `docs/audit/validation-review21-2026-09-16.json`. Prior host V20 status/ledger are preserved under docs/history/*_THROUGH_V20.md.

Implemented: device-local clean TV with top recovery/diagnostics/paging, continued shared width/density synchronization, square 30px console/details links, viewer self-logout without general mutation rights, delayed history-export reauthorization, explicit export storage errors, and transactional still-current credential successor registration. No agent protocol/database migration or agent authority change.

Host executed `scripts/verify-audit21.sh --with-browser`: Go 1.27.1 race 478/714/27, 199 frontend tests, native processes 3/3 uid=1000, browser **50/50**. Auditor browser was BLOCKED_BEFORE_LOGIN; five new scenarios ran here. Vue omitted-Boolean `interactive` is default true so ordinary overview handles remain. UID isolation fixture NOT RUN. This is not a claim of completed v3 or a completed external security audit.

No agents must be replaced for V21. Preserve current identifiers, credentials, trust, history, shared TV profile and local console opt-in. `operation.retry_selected` and independent service-host self-update remain fail-closed. Full restore, physical TV, native boot, perimeter and fleet/soak gates stay open in `docs/RELEASE_COMPLETION_PLAN.md`.
