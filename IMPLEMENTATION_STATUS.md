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
