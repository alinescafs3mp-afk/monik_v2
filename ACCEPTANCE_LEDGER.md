# Current acceptance: Audit 11

Baseline `b110c2de7688d8d2d28f6c5498bb276c8825a1f8`. Auditor source tree `7326a43f273ecf3b6bca73971fb3aacaa0aa58a4`. Host extras versus that tree are listed below; they do not weaken product contracts. Source validation digest and scopes: `docs/audit/validation-review11-2026-09-14.json`.

| Check | Current evidence |
|---|---|
| Full Go race suite | HOST PASS 280 top-level tests / 352 pass events / 19 tested packages; 2 opt-in process tests skipped here, passed separately. |
| Baseline regressions | CONFIRMED: 3; all pass after correction. |
| Concurrency/restart repeats | HOST PASS: four selected tests, ten repetitions each. |
| Frontend | PASS: 103 Node helper/request tests. |
| TypeScript / Vite | PASS: tsc --noEmit and production bundle; not vue-tsc or visual acceptance. |
| Vet / module verification | PASS. |
| Linux / Windows amd64 | HOST PASS: four commands each; Windows is cross-compilation only. |
| Compiled Linux worker + supervisor | HOST PASS uid=1000 (setuid 65534 only when euid=0); real CPU/RAM, stop/restart/respawn and outage/spool recovery. |
| SSH fixture suite | HOST PASS: existing 8 encrypted SSH/PTY protocol fixtures, not native shell/browser proof. |
| New browser scenario | HOST PASS 29/29 Playwright 1.57.0 Chromium, including canary preview, durable pause/reload/resume and pending cancel. Auditor BLOCKED_BEFORE_LOGIN ERR_BLOCKED_BY_ADMINISTRATOR. |
| Native systemd/SCM boot, signed multi-machine rollout, power-loss recovery, physical TV | NOT RUN. |
| Full restore / service-host independent recovery | NOT IMPLEMENTED / NOT ACCEPTED. |
| Full fleet, long retention and 24h soak | NOT RUN. |

Host extras versus auditor tree `7326a43f` (do not weaken product contracts):

- Ledger/status/validation record the host Playwright and native uid=1000 runs. No product-code change was required after the patch.

The full suite first caught one obsolete expectation that update.resume must return an unimplemented error. Only that outdated assertion was replaced with real resume authorization/CAS/same-job/expiry/fault tests; generic retry remains unavailable. The intermediate failure log is retained in the package.

---

## Historical acceptance (not current-source execution)

# Current acceptance: Audit 10

Base `ce602d01b3bc335a94b194bf1c3c04e7e46a25ad`. Auditor source tree `13ac3da94fb53427edd338a9e7a897c4e937f420`. Host extras versus that tree are listed below; they do not weaken product contracts.

| Check | Current evidence |
|---|---|
| Full Go race suite | HOST PASS 254 top-level tests / 322 pass events / 19 tested packages; 2 opt-in process tests skipped here, passed separately. |
| Frontend | PASS: 96 Node helper/request tests. |
| TypeScript / Vite | PASS: tsc --noEmit and production bundle; not vue-tsc or visual acceptance. |
| Vet / module verification | PASS. |
| Linux / Windows amd64 | HOST PASS: four commands each; Windows is cross-compilation only. |
| Compiled Linux worker + supervisor | HOST PASS uid=1000 (setuid 65534 only when euid=0); real CPU/RAM, stop/restart/respawn and outage/spool recovery. |
| SSH fixture suite | HOST PASS: existing 8 encrypted SSH/PTY protocol fixtures, not native shell/browser proof. |
| Publication regressions | CONFIRMED: 4; all pass after correction. |
| Immutable download | PASS: signed TUF + actual HTTPS, A after B, tamper/replay reject; fixture bytes not executed. |
| Transaction faults | PASS: artifact/outcome/second-target rollback. |
| New browser scenario | HOST PASS 28/28 Playwright 1.57.0 Chromium, including Updates immutable-release preflight. Auditor BLOCKED_BEFORE_LOGIN ERR_BLOCKED_BY_ADMINISTRATOR. |
| Native systemd/SCM boot, power-loss recovery, physical TV | NOT RUN. |
| Full fleet, long retention and 24h soak | NOT RUN. |

Host extras versus auditor tree `13ac3da9` (do not weaken product contracts):

- Ledger/status/validation record the host Playwright and native uid=1000 runs. No product-code change was required after the patch.

Machine-readable: `docs/audit/validation-review10-2026-09-14.json`.

---

## Previous acceptance history

# Acceptance ledger: Audit 9

Baseline `efae8069f27f3ba2f951273796dbd99668db364d`, 2026-09-14. Auditor source tree `c2b2b5275ebbd9b1e1108edd2c99e789ca3c6554`. Host extras versus that tree are listed below; they do not weaken product contracts.

| Check | Current evidence |
|---|---|
| Full Go race suite | HOST PASS 232 top-level tests / 300 pass events / 19 tested packages; 2 opt-in process tests skipped here, passed separately. |
| Frontend | PASS: 88 Node helper/request tests. |
| TypeScript / Vite | PASS: tsc --noEmit and production bundle; not vue-tsc or visual acceptance. |
| Vet / module verification | PASS. |
| Linux / Windows amd64 | HOST PASS: four commands each; Windows is cross-compilation only. |
| Compiled Linux worker + supervisor | HOST PASS uid=1000 (setuid 65534 only when euid=0); real CPU/RAM, stop/restart/respawn and outage/spool recovery. |
| SSH fixture suite | HOST PASS: existing 8 encrypted SSH/PTY protocol fixtures, not native shell/browser proof. |
| New failing baseline regressions | CONFIRMED: 4; all pass after correction. |
| Notice API / persistence / bulk CAS | PASS: authenticated/CSRF, transactions, restart, new-error, one-connection and >200-row cases. |
| New browser scenario | HOST PASS 28/28 Playwright 1.57.0 Chromium. Auditor BLOCKED_BEFORE_LOGIN ERR_BLOCKED_BY_ADMINISTRATOR. |
| Native systemd/SCM boot, power-loss recovery, physical TV | NOT RUN. |
| Full fleet, long retention and 24h soak | NOT RUN. |

Host extras versus auditor tree `c2b2b527` (do not weaken product contracts):

- `internal/server/console.go`: `closeAll` waits for in-flight console sessions so their final audit writes finish before `Store.Close`. Without that wait this host's Go 1.27 TempDir cleanup failed on leftover SQLite WAL/SHM (`TestAudit8ConsoleSSHPTYInputResizeAndNoTranscript`).
- `web/src/styles.css`: `.table-wrap` and `.operation-read-control` are positioning contexts so the absolute `.sr` status span cannot expand `document.documentElement.scrollWidth` past the 390px viewport. The operations table still scrolls internally.

Machine-readable: `docs/audit/validation-review9-2026-09-14.json`.

---

## Historical evidence retained below

# Acceptance ledger: Audit 8

Source base `3873c9c` / app `4e0ae20`, 2026-09-14. Evidence refers to this corrected source, not historical CI and not production.

| Check | Actual result |
|---|---|
| Go race suite | PASS 211 top-level tests / 276 pass events / 19 tested packages; 2 opt-in native tests skipped here, passed separately. |
| Vet / Go module verification | PASS |
| Frontend tests | PASS 78, helpers and source contracts, not browser count. |
| TypeScript / Vite | PASS; not separate vue-tsc. |
| Linux / Windows build | PASS 4 commands per target; Windows compilation only. |
| Compiled Linux worker | HOST PASS uid=1000 (setuid 65534 only when euid=0); real CPU/RAM, clean stop/restart, upload outage and spool recovery. |
| Compiled Linux supervisor | HOST PASS uid=1000; killed child respawn, orderly stop/restart and spool recovery. |
| SSH fixtures | PASS 8 cases, encrypted SSH and PTY protocol, not native Bash/PowerShell. |
| Baseline failing regressions | CONFIRMED service.rename missing; invalid/null host snapshot; premature supervisor return. |
| Browser attempt | HOST PASS 24/24 Playwright 1.57.0 Chromium. Auditor BLOCKED_BEFORE_LOGIN ERR_BLOCKED_BY_ADMINISTRATOR. |
| systemd / SCM boot / power loss | NOT RUN |
| Actual Yandex TV / native SSH shell | NOT RUN |
| Supervisor self-update / full restore | NOT IMPLEMENTED / NOT ACCEPTED |
| Full fleet / long retention / 24h soak | NOT RUN |

Host extras versus auditor tree `64899f47` (do not weaken product contracts):

- `internal/install/linux_test.go`: after `WriteFile(..., 0644)` pin mode with `Chmod(0644)` because this host umask is 0077, so the assertion actually proves `inspectPrivateState` did not chmod the unrelated hard-link target.
- `tests/browser/audit.py`: identify the grouped Services row by `Переименовать: API` rather than `cell` name exactly `API`, because `ServiceNameEditor` shares that cell.

The package includes final complete logs and separate failing/incomplete reproduction logs. Never turn an interrupted command, a compiled binary, a helper assertion or an earlier CI pass into broader acceptance evidence.
