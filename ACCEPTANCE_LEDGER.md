# Acceptance ledger: audit 4

Exact reviewed base: `4f85de31f837dbdfeb77e9f562726de5f00b9d28` (tree `fdec03382ddab5f3ddcfe8984e9dc57a1d2788d7`), 2026-09-14.
Integrated on this host from that exact tree. The auditor's local browser was blocked; this integration re-ran the expanded fixture.

| Check | Result / scope |
|---|---|
| Baseline tree | Matched `fdec03382ddab5f3ddcfe8984e9dc57a1d2788d7`; `verify_package.py` 41 files PASS; `preflight.py` PASS |
| Baseline local race | Auditor: FAILED existing TestReviewBrokenCandidateRestoresPreviousWorker before the patch |
| Before-fix crypto | Auditor: three targeted failures before the patch |
| Final Go race | PASS: `go test -race -count=1 ./...` (18 tested packages) after UI rebuild |
| Go vet / modules | PASS / go mod verify PASS |
| Frontend | 39 Node tests PASS (38 from the patch plus queued-trial evidence); tsc --noEmit PASS; Vue/Vite production build PASS |
| Linux builds | After commit: matching dist SHA recorded in deploy notes |
| Windows builds | Cross-compile only; native execution NOT RUN |
| Browser scenario | PASS synthetic 11/11 on compiled Vue + Go fixture (Chromium, `--no-sandbox`, loopback HTTPS). Integration extras: CheckEditor `aria-label`; queued `check.trial` evidence no longer looks like a completed agent result |
| Native systemd/SCM, power loss, capacity, 24h, complete v3 | NOT RUN |
| Self-updating supervisor / full protected restore / aggregates | Not fully implemented, not accepted; fail-closed retained |
| Owner deployment | DEPLOY AFTER COMMIT (server first, then worker; confirm `http_custom_v1`) |

Go 1.27.1 and Node 22.23.2, locked dependencies, no new packages. Fixture telemetry is not a real fleet. Native Windows SCM/boot and 24h soak remain NOT RUN. Telegram stays deferred.
