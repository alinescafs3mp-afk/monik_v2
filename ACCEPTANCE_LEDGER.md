# Acceptance ledger: Audit 7

2026-09-14. Exact source baseline `5d17aaab6b3f4259820b089c28a7f4367e065372`, tree `1c2aaf9da364c541471bb45b9ec43bf3329a65d6`. Go 1.27.0, Node 22.16.0; baseline lockfiles. This ledger replaces current acceptance claims, not historical evidence in Git.

| Check | Actual outcome and scope |
|---|---|
| Baseline race | FAILED on retention-test timing under contention; not claimed clean |
| Final race | PASS: 194 top-level tests, 257 test/subtest pass events, 18 packages; package parallelism 1, GOMAXPROCS=2 |
| Vet / module verify | PASS / PASS |
| Frontend | PASS: 72 Node tests, both behavioral and identified source contracts |
| TypeScript | PASS tsc --noEmit; no separate vue-tsc claim |
| Production UI | PASS Vue/Vite build |
| Linux amd64 | Four commands built; no native installation claim |
| Windows amd64 | Four commands cross-built; no SCM or boot evidence |
| Selective monitoring | Local HTTP listener: repeated disabled discovery generated zero requests; SQLite/API tests preserve settings, scope, pin independence and desired/applied truth |
| Session lifecycle | Cookie flags/absolute expiry, ordinary versus remembered lifetime, own-session list and other-session revocation, logout and fake-clock cases PASS |
| TV/mobile | Pure sizing/paging/aging tests and compiled responsive components PASS; real physical layout acceptance is NOT RUN |
| Browser syntax/fixture build | PASS / PASS |
| Current browser walkthrough | HOST PASS 22/22 Playwright 1.62.0 Chromium (`PLAYWRIGHT_HOST_PLATFORM_OVERRIDE=ubuntu24.04-x64`); auditor run stayed BLOCKED_BEFORE_LOGIN |
| Previous 19/19 browser evidence | Historical baseline only, not inherited for Audit 7 |
| Full patch/source reproduction | Auditor tree `15f9c493` reproduced; host extras then change the tree (browser locators, ledger) |
| Owner production and TV | Physical Yandex/TV NOT RUN; live LAN deploy is a later host step, not this source ledger |
| Native privileged install, independent service-host recovery, full protected restore, fleet load, 24h soak | NOT RUN; mandatory implementations also remain incomplete |

Host extras versus auditor tree `15f9c493`: pin/monitor/machine-overview checkboxes commit only after server CAS, so the browser scenario uses `click` plus `expect` instead of Playwright `check()`/`uncheck()` (those require an immediate native toggle). Assertions of persisted pin independence, paused desired config, remembered session, and two TV rows at 960×540 are unchanged.

Machine-readable evidence: `docs/audit/validation-review7-2026-09-14.json`. Logs accompany the source archive. No audit binary is distributed as a signed production release. Host race: 194 top-level / 257 events / 18 packages (`go test -race -count=1 ./...`), Node 72, tsc, Vite, vet, `go mod verify`.
