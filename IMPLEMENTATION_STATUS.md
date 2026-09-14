# Implementation status after cumulative source audit 3

Baseline: `2f63c0b59498beb9436c092554cfef10e1942489`. This source includes all audit 2 changes plus audit 3. Read `docs/AUDIT_REVIEW_3_2026-09-14.md`, `docs/RELEASE_COMPLETION_PLAN.md` and `ACCEPTANCE_LEDGER.md`.

**PRE-RELEASE. Not all owner v3 requirements are complete. No production deployment or remote write was performed in audit 3. Historical reports describe historical states, not this revision's acceptance.**

| Area | Current implementation / remaining boundary |
|---|---|
| Overview/navigation | Dense rows, optional cards, persistent per-machine overview pin, collapsible sidebar and visible chart graduations preserved. Current visual acceptance NOT RUN. |
| Machine history | Six ranges, extrema/gaps, raw point lookup, bounds/units/timezone. True raw JSON export now implemented with strict limits. Long-term aggregates incomplete. |
| Problems history | Interval-overlap search, lifecycle/severity/metric/entity filters, stable pagination and fixed-history controls implemented. Not a bitemporal historical-status engine. |
| Service feedback | Bounded HTTP metadata/known health fields preserved. No raw response bodies; self-reported health is not full app proof. |
| Enrollment | Atomic code/identity/config binding and persisted-proof retry implemented. Selected URL retained; existing configuration protected. Server-first rollout needed for new client proof. Native concurrent installation/recovery NOT ACCEPTED. |
| Telemetry/config | Existing atomic ingest/dedup preserved; desired revisions now also published transactionally into confirmation history. New spool records keyed by transport identity; current worker reads legacy and v2 queue. |
| UI feedback/auth | Coalesced refresh, network versus authentication distinction, historical download links and no nonexistent-incident acknowledgement added. Recent-auth UX, native/browser acceptance pending. |
| Worker updates | Prior target-link/signature/activation corrections retained; full immutable publication, cohort orchestration and native crash recovery remain open. |
| Service-host updates | Explicitly unavailable until independent native replacement/recovery exists. Mandatory release blocker. |
| Rebind/trust/credentials | Prior bounded state-machine corrections retained. Full offline time/cancel/expiry/restore/native evidence incomplete. |
| Rules/maintenance/retry/resume | `rule.save`, `maintenance.set`, `operation.retry_selected`, `update.resume` remain explicitly unavailable. `history.export` is no longer on that list. |
| Storage/backup/restore | Recent raw data and bounded export are implemented; large retention jobs, long aggregation and complete protected-controller restore are NOT ACCEPTED. |
| Native fleet/load | Linux and Windows compile. Native SCM/systemd boot/recovery, full v3 battery, fleet-scale load and 24h soak NOT RUN here. |

Default initial URL remains `https://46.120.103.61:8777`; the actually selected/persisted controller address takes precedence. Never reset an existing deployment to a compiled default. Telegram and general remote OS commands remain out of scope.
