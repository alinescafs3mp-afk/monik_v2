# Start here: Monik V12 stabilization handoff

Owner authorization: deliver a cumulative source archive; the auditor did NOT write to GitHub or deploy. Local Grok integrates, verifies, commits and deliberately deploys after the gates pass.

## Inputs

- Repository: `alinescafs3mp-afk/monik_v2`, target branch `main`.
- Exact baseline commit: `c2229685f76551bcbdb0bb89e1fe1adc16e28878`.
- Exact baseline tree: `5b091979aa6ed5253397dfdfcd2dc0c8c0758fc5`.
- One delta patch: `monik-audit12-fixes.patch`. Full source ZIP is a reference snapshot, NOT permission to overwrite a dirty repository.
- All previously integrated v1-v11 features are in the snapshot. Do not reapply older patch packages.
- The outer manifest records final tree, patch hash and source ZIP hash without circular references.

## Required order

1. Read `AUDIT_REVIEW_12_2026-09-15.md` and `V12_DEPLOYMENT_GATE_RU.md`, then verify the package with `python3 verify_package.py`.
2. Run `python3 preflight.py /absolute/path/to/repository`. It is read-only and checks a clean tracked/index/untracked source state, the baseline tree and `git apply --check`. If main moved, deliberately merge changes in a separate clean worktree; do not force-reset concurrent work.
3. Apply exactly this patch, inspect it, build UI before server and run `scripts/verify-audit12.sh --with-browser` in an allowed environment. The browser case was NOT accepted in the audit environment. The upstream baseline CI failed a brittle fsync timing test; do not assume its build/browser steps ran.
4. Preserve all new failure assertions. In particular the genuine signed native update test must prove a new session/digest AND observation, then a failing candidate's verified rollback. Never replace it with synthetic receipts to save time.
5. Stop on any test failure. Record exact source/version/environment. After integration use the real commit metadata for released binaries, not the auditor's temporary build label.
6. Before deployment protect complete controller AND pilot-agent state. Server and UI first; matched worker/supervisor next. Existing registrations, endpoints, encrypted secrets, overview pins, service labels and custom requests are preserved. No repeated setup or identity reset.
7. Execute the systemd/boot/independent-access pilot gate before spreading to remote servers. Do not downgrade a server with active canary/held jobs, do not start two copied controller databases, and do not turn an error about lost keys into fresh initialization.

## Important changes

Exact receipt acknowledgement; immutable configuration revisions; startup reservation of in-flight lifecycle jobs; snapshot-safe TLS clients; strict corruption guards; local process locks; joined shutdown; verified rollback persistence; original controller identity retention; encoded SQLite filenames; frozen rollout membership checks; real asset 404/cache behavior and visible navigation errors. No new runtime dependency, no monitoring-agent shell, no expansion of SSH target authority.

## Acceptance boundaries

The suite and process evidence apply to the tested subset. Native Linux process and real signed worker update are not systemd/reboot acceptance. Windows is cross-build only. Full protected restore, independent supervisor self-recovery, long-term aggregates/soak and generic selected retry remain unfinished. Keep fail-closed guards. Treat acknowledgement/read metadata as attention control, never successful execution or permission to resume failure.

The package's raw logs use actual environment timestamps. Package documentation uses session date 2026-09-15. Do not alter logs to make them match.
