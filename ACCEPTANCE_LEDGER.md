# V20 acceptance ledger (2026-09-15)

Reviewed commit `0385030878ec58e0f7a8bf1b95ab1f38fd1cde06`, tree `b948195118d1b08bf765106ee55d78b52555a7ce`. Patch applied to exact auditor tree `411474a6c760ade2c20e25b95a3de58fcfec7461`. Previous V19 local host evidence is preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V19.md`. The separate baseline GitHub CI failed after 42 browser cases; neither report cancels the other. Auditor browser was BLOCKED_BEFORE_LOGIN; host executed that gate. See `docs/audit/validation-review20-2026-09-15.json`.

| Gate | Actual host evidence |
|---|---|
| Baseline regressions | Eleven groups fail on the unchanged baseline; same checks pass on corrected source. Additional first-draft Origin edge case fixed during self-review. |
| Go 1.27.1 race | 465 top-level PASS, 691 test/subtest/seed PASS events, 27 packages. 7 opt-in/fixture tests skipped in the ordinary full suite; native process cases run separately. |
| Frontend | 188 Node tests PASS; tsc and production Vite build PASS. Includes source assertions; not vue-tsc. |
| Repeats/fuzz | New V20 groups five shuffled race repetitions PASS. Origin fuzzer 344,416 executions during requested 15s PASS. |
| Modules/static | go vet and go mod verify PASS. Lockfiles unchanged. Not a CVE audit. |
| Build | Four binaries each for Linux/Windows amd64; one-file Linux amd64/arm64 templates rebuilt after commit. Cross-compilation is not native Windows acceptance. |
| Native processes | PASS 3/3 uid=1000, including signed replacement and failed-candidate rollback. Not systemd/boot. |
| Console identity | Source/PTY/frame tests PASS. UID isolation fixture NOT RUN (needs disposable root). Broker was not dropped to UID65534 on this host. |
| systemd unit syntax | PASS only. No native service activation/install/boot. |
| Browser | Host 45/45. Includes V19 two-context shared TV geometry/density/autoplay, lost-save identity, SSE fallback, edit conflict, local mode/390px isolation, plus prior V18 remote/console and V17/V16 cases. Fixture loopback only. Assertions were not weakened. |
| ICMP, physical TV, real perimeter, load/soak | NOT RUN. No full restore or independent service-host recovery claim. |
| Package | Exact target tree `411474a6c760ade2c20e25b95a3de58fcfec7461` before host extras. |

Host extras versus auditor tree `411474a6`: this ledger, `IMPLEMENTATION_STATUS.md`, and `docs/audit/validation-review20-2026-09-15.json`. No locator, origin, CSRF, TLS or geometry check was weakened. `operation.retry_selected` and independent service-host self-update stay fail-closed. Existing agents need no binary replacement for V20. Server/UI-only update: preserve enrolled identities, CA/TLS, journals, shared TV profile and local console opt-in. Installer templates for future downloads must match the deployed server build.
