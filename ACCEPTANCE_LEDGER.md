# V18 current acceptance (2026-09-15)

Baseline `afdbbd2573b68d99eb3115032f62f6b33681a5cd`, tree `82fef7293be36b76bb587682425a0ed0685230da`. Auditor browser was BLOCKED_BEFORE_LOGIN; host executed that gate. Previous V17 ledger is `docs/history/ACCEPTANCE_LEDGER_THROUGH_V17.md`. See `docs/audit/validation-review18-2026-09-15.json`.

| Gate | Actual host evidence |
|---|---|
| Baseline | Exact tree `82fef7293be36b76bb587682425a0ed0685230da`. Patch applied to auditor tree `c365dfe8f74fdd69556ff63fa1979725edde156c`. |
| Go 1.27.1 race | PASS: 431 top-level including fuzz seeds, 610 test/subtest/seed PASS events, 27 packages. Opt-in native/ICMP/UID/unit-syntax skip in `./...`; native process cases and unit syntax run separately. |
| Frontend | PASS: 167 Node tests, `tsc --noEmit`, Vite production UI. Not vue-tsc. |
| V18 security repeats | PASS: 5 shuffled race runs of `TestV18`/`TestV17Console` in server, agentconsole, install. |
| Fuzz | PASS: `FuzzV18TerminalFrames` 15s, 876765 execs, 0 crash. |
| Modules/static | go vet and go mod verify PASS. Lockfiles unchanged. |
| Linux/Windows build | Four binaries per amd64 platform; Windows CROSS-COMPILE ONLY, no ConPTY. Credential-free installer templates linux-amd64 and linux-arm64 rebuilt after commit. |
| Browser | Host 41/41. Includes V18 TV remote OK/arrows/native +/−, transport selection, prior V17 geometry and V16 scroll/focus. Fixture loopback only. |
| Loopback ICMP | Host `MONIK_NATIVE_PING=1` PASS to 127.0.0.1 as uid=1000. Not systemd AmbientCapabilities. |
| Native processes | PASS 3/3 uid=1000, including signed update and failed-candidate rollback. Not systemd/boot. |
| systemd unit syntax | PASS `TestV18SystemdUnitSyntax` with `systemd-analyze`. Not socket activation, cgroup kill, or boot. |
| UID isolation fixture | NOT RUN: requires disposable root to drop test UIDs. Not run as root on the live controller. |
| Physical TV, native service boot, soak | NOT RUN. Console was not enabled on lan-host or the first remote. Existing agents were not re-enrolled. |

Host extras versus auditor tree `c365dfe8`: this ledger, `IMPLEMENTATION_STATUS.md`, `docs/audit/validation-review18-2026-09-15.json`, Playwright heading/transport alignment, and TV remote tests that pick an adjacent-track direction with slack then restore `display=auto`. Assertions were not weakened. `operation.retry_selected` and service-host self-update stay fail-closed. Agent console stays opt-in and locally enabled.
