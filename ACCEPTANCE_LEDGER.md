# Current acceptance: V14 (2026-09-15)

Baseline `b969c6a34d3852687bd3aa3c73212ec4e183f55f`. Prior ledger: `docs/history/ACCEPTANCE_LEDGER_THROUGH_V13.md`. Exact new evidence: `docs/audit/validation-review14-2026-09-15.json` and the delivery evidence directory.

| Gate | Actual evidence |
|---|---|
| Baseline | Original source tree matches GitHub. Baseline Go race and 108 Node checks/types/UI pass. |
| Regression reproduction | FOUR compatible named tests fail before fixes: accumulating inventory, per-port desired revisions, lost SANs on renewal, malformed Linux listener omission. Host parsing guard passes before and after, not counted as a defect. |
| Final Go race | PASS: 353 top-level pass events including 3 fuzz targets; 478 test/subtest/seed pass events; 23 packages. 3 opt-in process tests skip here, run separately. |
| Repeated V14 | PASS: 10 shuffled race-enabled repetitions across 6 packages. |
| Windows-table fuzz | Auditor: 15s, 313,253 execs, 2 workers. Host: 15s, 788,015 execs, 2 workers. Bounded parser-only target. |
| Frontend / types / UI | PASS: 115 Node helper/request/source-structure tests, `tsc --noEmit`, production Vue/Vite. Not separate vue-tsc or visual acceptance. |
| Vet / Go modules | PASS. Lockfiles unchanged; no vulnerability-database scan claimed. |
| Linux/Windows build | All four binaries each. Windows is CROSS-COMPILE ONLY. |
| Real kernel listener | PASS: actual Linux listen/close/reopen plus actual SQLite and API views. Grace time is simulated; other ports not probed. |
| Actual Linux processes | PASS 3/3. Auditor UID 65534; host UID 1000. Compiled binaries: metrics, restart/offline queue, supervisor respawn/shutdown, signed worker replacement and failed-candidate recovery. Not systemd/SCM/boot. |
| Browser | Auditor BLOCKED_BEFORE_LOGIN (`ERR_BLOCKED_BY_ADMINISTRATOR`). Host executed: 33/33, including hidden inactive discoveries, selected missing endpoint remaining visible, profile origin independent of saved LAN, TLS mismatch blocking download, pending old-check, missing-asset 404/no-store, and 390px Add-machine after wrapping install-command `<pre>`. Assertions were not weakened. Fixture loopback only. |
| External owner endpoint | NOT ACCESSED. Profile preflight checks loaded cert, not external reachability. |
| systemd/SCM/boot, real TV, power loss, fleet and 24h soak | NOT RUN. |
| Full protected restore / independent supervisor recovery / long aggregates | STILL OPEN. |

Source, patch reproduction and package checksums are outside the source snapshot to avoid circular hashes. Runtime timestamps remain as generated. No GitHub writes or live changes were made.
