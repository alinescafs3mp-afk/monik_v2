# V17 current acceptance (2026-09-15)

Baseline `35fccbb0265777b12c2893667ef2fea33e953928`, tree `aa0cdf94c9b07a60df715463b863cfe0721052f0`. Auditor browser was BLOCKED_BEFORE_LOGIN; host executed that gate. Previous V16 ledger is `docs/history/ACCEPTANCE_LEDGER_THROUGH_V16.md`. See `docs/audit/validation-review17-2026-09-15.json`.

| Gate | Actual host evidence |
|---|---|
| Baseline | Exact tree `aa0cdf94c9b07a60df715463b863cfe0721052f0`. Patch applied to auditor tree `5eea57c01f8ffe86c2fbbd2225d2bc9d0bb99332`. |
| Go 1.27.1 race | PASS: 395 top-level including fuzz seeds, 555 test/subtest/seed PASS events, 26 packages. Four opt-in tests skip in `./...`; three native process cases run separately. |
| Frontend | PASS: 158 Node tests, `tsc --noEmit`, Vite production UI. Not vue-tsc. |
| Regressions before/after | Seven scenarios FAIL against exact baseline `35fccbb` source (`MONIK_TEST_BASELINE`) and PASS after correction. |
| Repetition | 10 runs of 21 new regression/geometry/composable tests, 0 failures. Isolated 10-shuffle race V15 (7 packages) and V16/rollout (3 packages) PASS. |
| Pointer/geometry | Node: numeric/composable PASS. Browser: real pointer drag, keyboard, Escape, reload persistence, header/row alignment, service columns of three, TV 960x540 footer fit, isolated TV prefs, phone 390 reflow. |
| Modules/static | go vet and go mod verify PASS. Lockfiles unchanged. |
| Linux/Windows build | Four binaries per amd64 platform; Windows CROSS-COMPILE ONLY. Credential-free installer templates linux-amd64 and linux-arm64 rebuilt after commit. |
| Browser | Host 39/39. Includes V17 drag/keyboard/TV/phone plus prior V16 scroll/focus, unblinking Refresh, history-503 editor, installer/inventory/404/TV cases. Fixture loopback only. |
| Loopback ICMP | Host `MONIK_NATIVE_PING=1` PASS to 127.0.0.1 as uid=1000. Not systemd AmbientCapabilities, not 8.8.8.8, not a managed unit. |
| Native processes | PASS 3/3 uid=1000, including signed update and failed-candidate rollback. Not systemd/boot. |
| Physical TV, native service boot, soak | NOT RUN. Existing agents were not re-enrolled. `repair-icmp-linux.sh` was not executed here. |

Host extras versus auditor tree `5eea57c`: this ledger, `IMPLEMENTATION_STATUS.md`, and `docs/audit/validation-review17-2026-09-15.json`. No product-code change after the patch. Assertions were not weakened. `operation.retry_selected` and service-host self-update stay fail-closed. Production Go/schema/protocol are unchanged; no agent reinstall is required for resizing.
