# V16 current acceptance (2026-09-15)

Baseline `c671137d16269e568061a478b12fe3d4f3e7c82f`, tree `e4b29e07bce356322cd116bebd333841a50ac45a`. Historical ledger is `docs/history/ACCEPTANCE_LEDGER_THROUGH_V15.md`. Auditor browser/ICMP were blocked; host executed those gates. See `docs/audit/validation-review16-2026-09-15.json`.

| Gate | Actual host evidence |
|---|---|
| Baseline | Exact tree `e4b29e07bce356322cd116bebd333841a50ac45a`. Patch applied to auditor tree `fe89f860e196b5d71d566e62d0dae0917c3d8b50`. |
| Go 1.27.1 race | PASS: 395 top-level including fuzz seeds, 555 test/subtest/seed PASS events, 26 packages. Four opt-in tests skip in `./...`; three native process cases run separately. |
| Frontend | PASS: 137 Node tests, `tsc --noEmit`, Vite production UI. Not vue-tsc. |
| New interaction regression | Node: exact SFC watcher, eight passive snapshots do not refocus. Browser: four real inventory polls preserve scroll, focused node and draft; manual Refresh does not blink. |
| Repeated | PASS: 10 shuffled race V15 (7 packages) and V16/rollout (3 packages). |
| Native processes | PASS 3/3 uid=1000, including signed update and failed-candidate rollback. Not systemd/boot. |
| Linux/Windows build | Four binaries per amd64 platform; Windows CROSS-COMPILE ONLY. Credential-free installer templates linux-amd64 and linux-arm64. |
| Modules / static | go vet and go mod verify PASS. Lockfiles unchanged. |
| Browser | Host 37/37. Includes V16 scroll/focus/draft, unblinking Refresh, 3-row service columns at 1440/390/960/1920, plus prior inventory/installer/404/TV cases. Fixture loopback only. |
| Loopback ICMP | Host `MONIK_NATIVE_PING=1` PASS to 127.0.0.1 as uid=1000. Not systemd AmbientCapabilities, not 8.8.8.8, not a managed unit. |
| Native systemd/SCM, boot, TV, soak | NOT RUN. Unmanaged lan-host was not re-enrolled and did not receive `repair-icmp-linux.sh`. |

Host extras versus auditor tree `fe89f860`: this ledger, `IMPLEMENTATION_STATUS.md`, `docs/audit/validation-review16-2026-09-15.json`, and `tests/browser/audit.py` asserting history failure on the charts tab then returning to the still-open editor. Assertions were not weakened. `operation.retry_selected` and service-host self-update stay fail-closed.
