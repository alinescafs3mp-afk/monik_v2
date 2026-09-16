# V21 acceptance ledger (2026-09-16)

Reviewed base `3303e991a2624c945662355e22fa7cfb8ef06a6a`, exact tree `f319e9b12f6e4091a95dea162046f1e509fe2078`. Patch applied to exact auditor tree `12d287abd20118dbae11c225cb759f611bca0f38`. Previous V20 host ledger is preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V20.md`; it is NOT the V21 browser acceptance. Auditor browser was BLOCKED_BEFORE_LOGIN; host executed that gate. See `docs/audit/validation-review21-2026-09-16.json`.

| Gate | Actual host evidence |
|---|---|
| Go 1.27.1 race | 478 top-level PASS, 714 test/subtest/seed PASS, 27 tested packages. Opt-in/helper skips in the ordinary suite; native process cases run separately. |
| Frontend | 199 Node tests PASS; tsc and production Vite build PASS. Includes true composable/SFC execution plus one extra omitted-Boolean header case; not vue-tsc or pixel geometry. |
| Repeats | New V21 server/storage tests 10 shuffled race passes; 47 UI tests repeated 10 times. |
| Reproductions | Five baseline groups, nine failing leaf cases, corrected final tests PASS. |
| Static/dependencies | go vet and go mod verify PASS. Lockfiles unchanged. Not a CVE audit. |
| Build | Four binaries each Linux/Windows amd64; credential-free installer templates linux-amd64/arm64 rebuilt after commit. Windows is cross-compilation only. |
| Native processes | PASS 3/3 uid=1000, including signed replacement and failed-candidate rollback. Not systemd/boot. |
| Console UID fixture | NOT RUN (needs disposable root). `TestV18NativeActivatedPeerUIDAndKeyIsolation` skipped. |
| systemd unit syntax | PASS with `MONIK_SYSTEMD_VERIFY=1`. Does not install or start any service. |
| Browser | Host 50/50. Includes five new clean-TV scenarios plus prior V20/V19/V18/V17/V16 cases. Fixture loopback only. CSP `script-src 'self'` was not weakened. Physical TV/Yandex NOT RUN. |
| Full release | NOT ACCEPTED: restore, independent service-host recovery, boot, external perimeter and soak gates remain open. |

Host extras versus auditor tree `12d287abd20118dbae11c225cb759f611bca0f38`: this ledger, `IMPLEMENTATION_STATUS.md`, `docs/audit/validation-review21-2026-09-16.json`, Vue Boolean default so ordinary overview resize handles stay visible, CSP-safe browser waits, and restoring `monik:display=auto` after the clean-TV fixture. No locator, origin, CSRF, TLS or geometry check was weakened. Existing agents need no binary replacement or re-enrollment. Deploy matched server/UI, and rebuild matching installer templates for later downloads. Hiding TV controls is a local presentation choice, NOT a restricted credential or shell-policy change.
