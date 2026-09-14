# Acceptance ledger: cumulative source audit 3

Session date: 2026-09-14. Source baseline `2f63c0b59498beb9436c092554cfef10e1942489`; prior audit 2 imported and retained. Integration commit on owner main; LAN deploy follows the commit. Native Windows SCM and 24h soak remain NOT RUN.

| Check | Result | Evidence / limits |
|---|---|---|
| Go race suite | PASS | 116 top-level tests, 148 test/subtest pass events, 16 tested packages, no failed events |
| Go vet | PASS | Actual `go vet ./...` |
| Frontend tests | PASS | 27 Node tests, including 7 new history/refresh/auth-classification cases |
| TypeScript | PASS | `tsc --noEmit`; not a claim of separate vue-tsc component checking |
| Production Vue/Vite | PASS | Actual embedded UI build with locked dependencies |
| Linux amd64 | PASS | All four programs built |
| Windows amd64 | PASS | All four cross-built; no native SCM/boot execution |
| Python browser script | PASS | `tests/browser/audit.py` against compiled `tests/fixtures/audit-server`; 10 checks including history export and incident filters |
| Git whitespace | PASS | Final patch also checked during packaging |
| Baseline source + imported audit2 tree | MATCH | Reproduced c1ac8f... and 0d15c9... before new changes |
| New failing regressions before fixes | CONFIRMED | Config revision journal, consumed code on conflict, enrollment proof retry, trailing JSON, same-timestamp spool collision |
| Enrollment behavior | PASS bounded integration | Local HTTPS test servers; no production endpoint |
| Historical incident search/export | PASS bounded tests | Real temporary SQLite rows, interval/cursor/limit/access checks; not long-retention/load evidence |
| Raw queue compatibility | PASS bounded tests | Current worker reads legacy + new format, exact acknowledgement and corrupt-file visibility; native downgrade tests pending |
| Updated real-browser walkthrough | PASS (synthetic loopback) | Chromium `--no-sandbox`; Problems filters needed `aria-label` so `get_by_label("Состояние")` resolves; not native-fleet evidence |
| Service-host self-update | NOT IMPLEMENTED | Explicit guard retained; mandatory release feature |
| Native systemd/SCM power-loss recovery | NOT RUN | Local process tests/cross-builds are not native installation evidence |
| Long-term aggregates/full protected restore | NOT ACCEPTED | Genuine incomplete features recorded in completion plan |
| Full v3 battery, fleet capacity, 24h soak | NOT RUN | No release-ready/capacity guarantee |
| Owner's installation | DEPLOY AFTER COMMIT | Existing `lan-host` is not re-enrolled; advertised URL stays `https://192.168.12.128:8777` |

Machine-readable evidence: `docs/audit/validation-review3-2026-09-14.json`. Complete logs and cumulative patch reproduction evidence are delivered with the archive. SDK: Go 1.27.0 and Node 22.16.0. Runtime timestamps report September 13 and are preserved as produced; package date follows the session date.
