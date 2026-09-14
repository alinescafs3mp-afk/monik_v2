# Acceptance ledger: Audit 6

2026-09-14. Baseline `e771f35343b0c3f9aaba7db446d2ffd1afc3aa97`, tree `a4e92aa2286f3540982463768bd45752fe025d5c`. No live-system mutation.

| Check | Actual result / limits |
|---|---|
| Baseline race suite | PASS: 158 top-level / 216 pass events / 18 tested packages |
| New defects reproduced before fixes | 2: acknowledged-warning escalation, corrupt service observations |
| Final Go race suite | PASS: 178 top-level / 239 test and subtest pass events / 18 tested packages |
| Go vet / Go module verify | PASS / PASS |
| Frontend Node suite | PASS: 57, including behavior and source-contract tests |
| TypeScript | `tsc --noEmit` PASS; no separate vue-tsc component typecheck |
| Production Vue/Vite | PASS |
| Linux amd64 | Four commands built, not a native installation proof |
| Windows amd64 | Four commands cross-built; SCM/boot NOT RUN |
| Rule/maintenance/admission | Actual temporary SQLite, handler tests and fake-clock boundaries PASS; not native-fleet evidence |
| Password/session consistency | Current password, CSRF, revocation and concurrent stale-login checks PASS |
| Fault injection | Committed policy plus failed operation-result write reports unknown; PASS |
| Bounded cleanup | 10,500 expired records, bounded first pass, visible lag and subsequent cleanup; PASS |
| Browser script syntax | PASS |
| Current browser | PASS on this host: 19/19 Playwright scenarios against the synthetic loopback fixture (rules persist, maintenance badge+cancel, admission_closed then recent-auth dialog with the same client_request_key, unread ack/unack, password change revokes cookies). Auditor environment was blocked before login. |
| Expanded v6 browser interactions | PASS as the 19/19 run above; synthetic fixture, not native-fleet visual acceptance |
| Full patch/source archive | Packaging verifies clean application on baseline; this host's extras change the tree after `fdabbd16` |
| Native privilege/install/update/recovery | NOT RUN; mandatory incomplete features remain |
| 50-agent/1000-check load, full v3 battery, 24h soak | NOT RUN |
| Populated-copy index migration | PASS: sqlite backup of the live controller DB (integrity ok, ~159 MiB, WAL/synchronous=2); new time indexes created in 70 ms on the copy. Not a protected full restore. |
| Disposable loopback smoke | PASS: unknown announce `admission_closed`; after owner window, pending; `rule.save` warning 88 persists; maintenance set/cancel; diagnostics WAL + synchronous=2. Isolated temp data dir. |

Host extras (not in the auditor tree): closed recent-auth dialog is not mounted on public pages and has no password field in the DOM until shown; login uses the labeled «Пароль» field; the synthetic acknowledgement incident is an HTTP service row so `rule.save` `policy_changed` does not hide it from the open/unread filter.

Toolchain actually used on this host: Go 1.27.1, Node 22.16.0 and the baseline lockfiles. No generated binary here is certified or signed as a production release. Native Windows SCM/boot, Linux systemd identity installer, independent service-host recovery, immutable rollout resume, full-controller restore, `operation.retry_selected`, `update.resume` and 24h soak remain NOT RUN / not implemented.
