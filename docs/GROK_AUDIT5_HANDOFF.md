# Monik Audit5: apply one cumulative package

Repository: `alinescafs3mp-afk/monik_v2`. Reviewed upstream main: `37dc23405606b24a82a629533311c19dae577a2a`; baseline tree `a3e00db6ddefa2fb2c2751e022d6a1cd1b2be7a9`.

This archive supersedes prior corrective archives for a repository already at this baseline. It contains the full source with earlier audits retained and ONE new patch against 37dc234. Do not apply older audit2/3/4 patches again. Read `AUDIT_REVIEW_5_2026-09-14.md` and `AUTO_ENROLLMENT_RU.md` first. No signed production executables are supplied.

## Integrate without discarding work

1. Run `python3 verify_package.py` in the extracted delivery directory. Inspect `PACKAGE_MANIFEST.json` for exact baseline and corrected trees.
2. On a clean integration worktree run `python3 /path/to/package/preflight.py /path/to/repo`. It reports dirty/base/already-applied state without modifying source. If main changed, integrate deliberately; never force-reset the owner's work or copy the full source ZIP over a dirty checkout.
3. On the matching base run `git apply --check /path/to/monik-audit5.patch`, then `git apply /path/to/monik-audit5.patch`. Review all changes. The standalone source ZIP is a reference alternative, not a second patch.
4. Build and test: `cd web && npm ci && npm test && npx --no-install tsc --noEmit && npm run build`; then from root `go test -race -count=1 ./...`, `go vet ./...`, `make dist`. Build UI before embedding it. Retain `internal/webui/dist/.gitkeep`, not generated bundles in Git.
5. Execute expanded `tests/browser/audit.py` with the repository fixture. The auditor's browser was blocked before login. Validate actual clicks, re-clicks, grouping, rename against new reports, history failure isolation and header layout. Do not weaken assertions or claim static source checks as browser execution.
6. Commit integrated code directly to main as authorized, preserving concurrent changes; rebuild released artifacts using that real commit, not audit seed 17cb8d7. Source publication is not permission to restart production silently.

## Deploy deliberately

Back up the full protected controller/selected-agent state. Upgrade controller first, then the selected worker. Existing agents preserve identity, endpoint, secrets and check definitions and must not be re-enrolled. The new automatic mode applies only to fresh trusted-profile setup.

A new machine receives a profile with its actual routable controller URL/CA and auto_discover:true. Run setup and the agent. Confirm it appears pending, verify the full fingerprint on the machine, approve in Machines, observe the first real report, then pin to Overview. Restart while pending and after approval; identity must stay unchanged. Before approval there must be no metrics/checks/jobs/secret reads. Test rejected, offline, stale, wrong-proof and lost-approval-response cases.

**Native installer limitation is real code work:** Linux account creation/state ownership and Windows service identity/ACL policy are not completed by the waiting loop. Finish those without granting unnecessary global privileges; prove actual service boot and state access before distributing to the fleet. The UI's service commands presume these prerequisites and compatible service-host binary. Quarantined installations must not be rolled back to workers that do not implement pending_registration.

## Owner-facing acceptance

Header: toggle, title, operations/search, user/logout. No permanent live subtitle or explanatory Overview sentence. Counters left aligned. OS/arch under machine name. Only unacknowledged open incidents counted, with actual failed service still failed. Services collapsed under machine IDs. Rename persists across report, reload and restart; stale edits conflict. Configure request opens correct editor visibly even on the same route and when graphs fail. Existing custom request/trial behavior remains intact.

All previous mandatory blockers in RELEASE_COMPLETION_PLAN.md remain. Keep fail-closed guards until actual implementation and native evidence exists. Telegram stays deferred; do not introduce shell execution, host reboot or arbitrary service remediation.
