# Current implementation: Audit 11, PRE-RELEASE

Baseline `b110c2de7688d8d2d28f6c5498bb276c8825a1f8`. Implements durable worker rollout canaries by OS/architecture, bounded later waves, fresh-identity observation, persisted pause/resume, automatic failure blocking, held-job cancellation and atomic delivery claims. Existing v10 immutable publication/download remains unchanged. No owner installation was contacted or changed.

New `update.pause` and `update.resume` operate on an existing rollout revision, retaining frozen job IDs. Generic `operation.retry_selected` is still guarded. Read/unread never authorizes a failed rollout to continue. Declared limits are not capacity evidence. Workers already advertising `immutable_release_v1` need no new registration for controller-side batching.

See `docs/AUDIT_REVIEW_11_2026-09-14.md`, `docs/V11_ROLLOUTS_RU.md` and `docs/audit/validation-review11-2026-09-14.json`. Auditor browser execution was blocked before login; host Playwright 1.57.0 ran the full fixture including the new canary/wave pause/resume/cancel scenario (29/29). Native process tests ran as uid=1000. These are not systemd/SCM/boot, signed multi-machine rollout, or physical-TV acceptance. Native fleet upgrade/boot/power loss, service-host recovery, full protected restore and long-term history remain open. Do not downgrade a server with active new-format rollout jobs. Generic `operation.retry_selected` remains guarded.

---

## Historical implementation records (not current acceptance)

# Current implementation: Audit 10, PRE-RELEASE

Baseline ce602d0. Implements immutable signed release storage/URLs, transactionally committed catalogue/trust/result, capability-gated complete update envelopes, corruption rejection and Updates preflight feedback. The importer no longer calls the legacy mutable ImportTrusted path. No production installation was changed by this source audit.

Existing agents continue monitoring, but new-style rollouts require a one-time protected local upgrade to advertise immutable_release_v1. Existing legacy catalogue entries require valid signed re-import. Root/key rotation, native independent service-host recovery, full protected restore, cohort orchestration, long aggregates and actual fleet acceptance remain incomplete. See docs/AUDIT_REVIEW_10_2026-09-14.md and docs/RELEASE_COMPLETION_PLAN.md.

Evidence: docs/audit/validation-review10-2026-09-14.json. Auditor browser execution was blocked before login; host Playwright 1.57.0 ran the full fixture including the new Updates immutable-release assertions. Native process tests ran as uid=1000. These are not systemd/SCM/boot or physical-TV acceptance.

---

## Historical implementation status

# Implementation status after Audit 9

**PRE-RELEASE.** Baseline `efae806`; this corrective source adds durable operation read/unread, versioned bulk acknowledgement, full-journal counters, filtered pagination and atomic target/job/result/notice publication. Read never means success or recovery. New failures re-open attention; ordinary progress and removal of known waiting reasons do not.

All previous integrated TV, monitoring-selection, service-name, SSH and agent features are preserved. No agent permission or re-enrollment change is required. Server/UI must be built from matching source. Current measured evidence is in `docs/audit/validation-review9-2026-09-14.json`. Auditor browser execution was blocked before login; host Playwright 1.57.0 ran the full fixture including the new operation-read scenarios.

Independent supervisor self-update/recovery, full protected controller restore, immutable release/cohort orchestration, long aggregates and native Windows provisioning remain open. `operation.retry_selected` and `update.resume` remain guarded. Operation read/unread does not substitute for those features.

---

## Previous implementation context (historical)

# Implementation status after Audit 8

**PRE-RELEASE.** Integration base `3873c9c`, application base `4e0ae20`. Host extras vs auditor tree `64899f47` are recorded in `ACCEPTANCE_LEDGER.md`. Read `docs/AUDIT_REVIEW_8_2026-09-14.md`.

| Area | State |
|---|---|
| Dense TV / responsive UI | New 10px TV default, aligned menu, bounded pagination; helpers/build tested; physical TV/browser visual acceptance NOT RUN. |
| Selected-scope priority | Red row/card and top ordering for host metric breaches/selected failed services; stable ties; other failures retain separate indicator. |
| Service names | CAS-backed owner labels in grouped and machine views; original endpoint/check identity retained. |
| SSH console | Optional protected fixed-target gateway, credentials per session, verified host keys, recent owner session, bounded WSS/PTY; eight SSH fixture tests pass; no agent shell. Native shell/browser NOT ACCEPTED. |
| Linux installer | Dedicated identity/private ownership/active worker slot/native tool/path checks implemented. Native systemd/boot NOT RUN; no complete transaction rollback. |
| Actual Linux processes | Compiled non-root worker and supervisor tested for telemetry, restart, child respawn, shutdown and spool recovery. |
| Windows | Cross-build passes; restricted account/ACL/SCM and recovery NOT ACCEPTED. |
| Existing monitoring/UI | Selective monitoring, pinning, custom requests, grouping, history, rules, maintenance, admission and session controls retained. |
| Service-host self-update | Independent replacement/recovery remains guarded and incomplete. |
| Release lifecycle | Immutable publication, cohort retry/resume and native crash recovery remain open. |
| Recovery/history | Full protected restore/reconciliation and long-term aggregates/capacity remain open. |

Existing registrations and the selected controller URL are preserved. Do not change the actual deployment to an old compiled address. Telegram remains deferred. Only the separately authorized SSH console changes the prior no-console scope; it does not grant OS authority to agents.
