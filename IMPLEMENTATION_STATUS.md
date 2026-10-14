# V22: Spark light, installed integration (2026-10-15)

Base `dc6511eeeb2df9b85b5be59a1b6074648f0cdc08`. The separate Linux ARM64 collector and working «Спарки» pages are installed. Three existing enrollment/trust identities, controller keys/settings/history, the original model arguments, scripts/TUI and current inference were preserved. Read the [operator guide](docs/V22_SPARK_LIGHT_RU.md), [acceptance ledger](ACCEPTANCE_LEDGER.md) and [sanitized native measurements](docs/audit/validation-v22-spark-2026-10-15.json).

Seven fixed profiles use one pre-existing Python Coordinator, StateStore, collection lock and memory reservation owner: the five original V4 profiles plus Qwen/GPT adapters. Root executes pinned source from sealed descriptors; the collector has no Docker/root access. The configured catalog is a fallback for other unsupported profiles, not the state of these seven integrations. Client generation budgets and model choices remain unchanged.

Typed actions bind current owner/session, policy epoch, profile revision, generation and immutable physical instance. Restart has separately authorized stop/start stages. Uncertain results and incomplete release remain explicit. Whole-receipt validation, bounded replay retention and completed-receipt reconciliation preserve deduplication. Fixed owner log sources use bounded tails/streams/buffers and protected redaction at read/emission boundaries. Negotiated bounded gzip preserves the original report/receipt identity and authenticated committed-ACK rules.

| Layer | Final observed result and limit |
|---|---|
| Source/build | Whole Go race/vet/module checks PASS; UI 215 tests/types/build PASS; all three installer templates and AMD64/ARM64 builds PASS. Copied Python 342 PASS; actual generated Python frames pass strict Go consumption; scoped independent reviews PASS. |
| Source identity | Code commit `b298673014e692fb3e897f5400aa45e6d5dc3322` exactly matches all 651 frozen build inputs, source ID `517cb70f62b92ede4dc936fe6295d50d67a9a34d1800d4a1e63407222725f39f`. This final acceptance documentation was added afterward. |
| Installed | Matched final controller/templates and both Spark monitor ELFs installed with full private backups and preserved locks/identities. No model start/stop/generation was needed for deployment. |
| Native observations/control | All seven profiles `quality=ok`: existing Ling READY, six stopped. Repeated start of already-running Ling kept its instance/generation; inactive legacy Qwen/GPT stop returned verified release. All three final operations completed/succeeded. This is not cold-start acceptance. |
| Logs/UI | Actual fixed Ling tail/live read and stale-epoch refusal PASS. Final served desktop/mobile/TV viewports: 15 screenshots, no errors/overflow. A quiet bounded log view was observed for the resource run; flood stress and physical TV NOT_RUN. |
| Whole resources | Idle CPU **23.64% of one core**, mean PSS **51.26 MiB**, sampled peak **83.26 MiB**, RSS peak **95.94 MiB**. Active log view: CPU **24.29%**, mean PSS **52.55 MiB**, sampled peak **84.37 MiB**, RSS peak **98.55 MiB**. Each actual run ~30 s, including product cgroups/helpers. Engineering aims were missed. |
| Wire | Actual duplex L2 lower bound **2379 B/s** idle / **2589 B/s** with one log view; packet drops zero. HTTP/TLS/control/ACK overhead included, physical framing partly excluded. Shared dockerd CPU outside product cgroups remains UNKNOWN. |
| Optimization | Seven observation helper launches became one fresh batch, retaining one fresh bind: nominal 8→2 helpers/round; actual CPU fell **51.95%** from the first F4 measurement. Fresh physical systemctl/Docker/GPU checks still dominate child cost; no effect/authority cache was introduced. |
| Unexecuted | Real seven-engine cold-start/stop/restart/cancellation/full-context inference, synthetic log flood, physical TV, fleet/soak, perimeter and full disaster restore remain NOT_RUN here. |

Resource figures are measurements, not target PASS. Targets <=32 MiB steady PSS, <64 MiB bounded-log PSS, <0.5% of one core and <2 KiB/s wire are engineering aims. Transient-child races make sampled memory peaks lower bounds; charged cgroup memory lifetime peaks are reported separately. Current Ling and prior owner-requested stops were preserved. Earlier F3 canaries remain separate evidence, not seven-engine acceptance or a full-product release.

Private deployment backups/partial receipts and raw logs are retained outside Git. Credentials, machine identifiers, enrollment profiles and personal handoffs are excluded from publication. Owned obsolete fixtures/reverse SSH and canaries were cleaned; the production monitors and owner inference remain running. No Astra/Friday message was sent.

## Historical V21: clean TV presentation and access correctness

Base `3303e991a2624c945662355e22fa7cfb8ef06a6a`. Read `docs/AUDIT_REVIEW_21_2026-09-16.md`, `docs/V21_CLEAN_TV_RU.md` and `docs/audit/validation-review21-2026-09-16.json`. Prior host V20 status/ledger are preserved under docs/history/*_THROUGH_V20.md.

Implemented: device-local clean TV with top recovery/diagnostics/paging, continued shared width/density synchronization, square 30px console/details links, viewer self-logout without general mutation rights, delayed history-export reauthorization, explicit export storage errors, and transactional still-current credential successor registration. No agent protocol/database migration or agent authority change.

Host executed `scripts/verify-audit21.sh --with-browser`: Go 1.27.1 race 478/714/27, 199 frontend tests, native processes 3/3 uid=1000, browser **50/50**. Auditor browser was BLOCKED_BEFORE_LOGIN; five new scenarios ran here. Vue omitted-Boolean `interactive` is default true so ordinary overview handles remain. UID isolation fixture NOT RUN. This is not a claim of completed v3 or a completed external security audit.

No agents must be replaced for V21. Preserve current identifiers, credentials, trust, history, shared TV profile and local console opt-in. `operation.retry_selected` and independent service-host self-update remain fail-closed. Full restore, physical TV, native boot, perimeter and fleet/soak gates stay open in `docs/RELEASE_COMPLETION_PLAN.md`.
