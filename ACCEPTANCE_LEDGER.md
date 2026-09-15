# V19 current acceptance (2026-09-15)

Baseline `776076273d677046b3a765e30d781f37b44957d5`, tree `61f3e23e62937654f65273aabb4f9ca53e288f4f`. Auditor browser was BLOCKED_BEFORE_LOGIN; host executed that gate. Previous V18 ledger is `docs/history/ACCEPTANCE_LEDGER_THROUGH_V18.md`. See `docs/audit/validation-review19-2026-09-15.json`.

| Gate | Actual host evidence |
|---|---|
| Baseline | Exact tree `61f3e23e62937654f65273aabb4f9ca53e288f4f`. Patch applied to auditor tree `bd8d7e3061d2b0d6e1a704a14cb8bb7536f23bc6`. |
| Go 1.27.1 race | PASS: 442 top-level including fuzz seeds, 628 test/subtest/seed PASS events, 27 packages. Opt-in native/ICMP/UID/unit-syntax skip in `./...`; native process cases run separately. |
| Frontend | PASS: 185 Node tests, `tsc --noEmit`, Vite production UI. Not vue-tsc. |
| V19 repeats | PASS: 5 shuffled race runs of `TestV19` in server and storage. 10 runs of 36 TV-profile/column Node tests, 0 failures. |
| Modules/static | go vet and go mod verify PASS. Lockfiles unchanged. |
| Linux/Windows build | Four binaries per amd64 platform; Windows CROSS-COMPILE ONLY. Credential-free installer templates linux-amd64 and linux-arm64 rebuilt after commit. |
| Browser | Host 45/45. Includes two-context shared TV geometry/density/autoplay, lost-save identity, SSE fallback, edit conflict, local mode/390px isolation, plus prior V18 remote/console and V17/V16 cases. Fixture loopback only. |
| Native processes | PASS 3/3 uid=1000, including signed update and failed-candidate rollback. Not systemd/boot. |
| UID isolation fixture | NOT RUN: requires disposable root to drop test UIDs. Not run as root on the live controller. |
| Physical TV, native service boot, soak | NOT RUN. Existing agents were not re-enrolled or replaced for V19. Shared TV profile does not change console opt-in. |

Host extras versus auditor tree `bd8d7e30`: this ledger, `IMPLEMENTATION_STATUS.md`, `docs/audit/validation-review19-2026-09-15.json`, and CSS/`TVProfilePanel` wrap so a 390px TV viewport cannot overflow. Assertions were not weakened. `operation.retry_selected` and service-host self-update stay fail-closed. Existing agents need no binary replacement for this feature.
