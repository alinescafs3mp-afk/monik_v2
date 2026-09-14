# Start here: integrate cumulative Monik audit 3 and complete the release

Repository: `alinescafs3mp-afk/monik_v2`. Owner prefers coherent commits directly to main. This package itself has NOT been published to main. Do not retry a rejected remote source publication through alternate encoding or services; the owner is providing this package for ordinary local implementation.

## Choose ONE patch route

- **Normal case: current main still based on 2f63c0b and audit2 not yet applied.** Use `monik-audit3-full.patch`. It includes all audit2 corrections and audit3. Do NOT apply the audit2 patch first.
- **Audit2 has already been integrated exactly or with deliberately reviewed local changes.** Use `monik-audit3-after-audit2.patch` as an alternative. Its base tree is `0d15c9f09840fbf6f20d450dafd97c7fc9305d40`.
- **Main advanced with other work.** Review the actual diff and integrate conflicts without resetting or overwriting the owner's source. Test the result. Do not force an exact tree hash by discarding concurrent work.

The full corrected source ZIP is a reference snapshot, not permission to unpack over a dirty repository. Never apply both patch variants in sequence. Source hashes/tree IDs are in `MANIFEST.json` at the package root.

## Preflight, build, test and commit

1. Read `00_START_HERE_RU.md`, run `python3 verify_package.py`, inspect `git status`, `git log`, remote main and protected-state backup readiness. Use a clean integration worktree and preserve concurrent work.
2. Run `git apply --check /absolute/path/to/the/chosen.patch`. Apply, inspect `git diff`, and stage only reviewed source/docs/tests. Do not include data directories, credentials, binary build output or dependency snapshots.
3. From `web`: `npm ci --no-audit --no-fund`, `npm test`, `npx --no-install tsc --noEmit`, `npm run build`. From root: `go test -race -count=1 ./...`, `go vet ./...`, `make dist`. The isolated audit used `make dist -o ui` only after already building matching UI.
4. Run the extended `tests/browser/audit.py` using the existing local fixture/CI workflow. Inspect real screenshots/results. These are synthetic observations against the real API/UI, not proof of a deployed native fleet. No weakening assertions to obtain green checks.
5. Read `docs/AUDIT_REVIEW_3_2026-09-14.md` for the newly fixed failure paths and protocol/state format notes. In particular, publish the server before new-client enrollment; preserve legacy and v2 spool state across rollback.
6. Commit the coherent integration directly to main as authorized by the owner. Record exact commit/run evidence, real native support and remaining limits. Do not claim this archive's local synthetic commit is a remote commit or a signed production release.
7. Deploy only through an owner-coordinated local procedure after protecting controller/agent state. A source audit does not authorize restarting, re-enrolling or rebinding the owner's sole running host merely to demonstrate a button.

## Complete what still blocks release

Follow `docs/RELEASE_COMPLETION_PLAN.md` in order: native lifecycle and independent supervisor recovery; immutable signed publishing/real rollout; full-state recovery and safe migration; actual custom rules/maintenance/command controls; measured long history and fair scheduling; native/browser UI acceptance. The original v3 requirement documents are copied under the package's requirements directory as reference, not as implementation evidence.

Keep fail-closed guards for `rule.save`, `maintenance.set`, `operation.retry_selected`, `update.resume` and service-host self-update until their actual behavior is implemented and proven. `history.export` is now real bounded raw JSON, not a stub; preserve its limits, authentication, provenance and truthful missing-data semantics.

Do not add Telegram, general remote commands, host reboot or unrelated app control. Do not replace the monitoring product with a bigger framework. Preserve overview selection/rows/sidebar/axes/feedback, and test your own changes as aggressively as these corrections.
