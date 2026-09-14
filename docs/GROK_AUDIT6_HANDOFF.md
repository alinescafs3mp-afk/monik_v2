# Grok: integrate Monik cumulative Audit 6

Repository: `alinescafs3mp-afk/monik_v2`. Intended branch: `main`.
Exact baseline commit: `e771f35343b0c3f9aaba7db446d2ffd1afc3aa97`.
Baseline tree: `a4e92aa2286f3540982463768bd45752fe025d5c`.

Read `AUDIT_REVIEW_6_2026-09-14.md`, `V6_OPERATIONS_RU.md` and the current `RELEASE_COMPLETION_PLAN.md`. Older audit reports describe old states. This package retains the existing integrations and adds one delta to the baseline; do NOT apply older audit2/3/4/5 patches again.

## Integrate without losing concurrent work

1. Read `PUBLICATION_STATUS.json` in the delivered archive; only that file states whether the assistant actually published a commit. An unreferenced Git tree or a report is not a source commit in main.
2. Run `python3 verify_package.py`. Run `python3 preflight.py /absolute/path/to/monik_v2`. This command reads the checkout and checks compatibility only; it does not apply or reset anything.
3. Use a clean worktree. If the remote main is still the baseline and the full v6 source has not been applied, run `git apply --check /absolute/path/monik-audit6.patch`, then `git apply --index /absolute/path/monik-audit6.patch`. Review changes. The exact baseline should reproduce the corrected tree in `PACKAGE_MANIFEST.json`.
4. If main advanced, preserve and integrate concurrent edits intentionally, resolve conflicts and rerun tests. Never force-reset the owner's work or unpack the source ZIP over a dirty repository. The ZIP is an independent reference snapshot, not an overwrite instruction.
5. Build the UI and code from the same tree. `cd web && npm ci && npm test && npx --no-install tsc --noEmit && npm run build`; then `go test -race -count=1 ./...`, `go vet ./...`, `go mod verify`, and `make dist`. The audit's `make dist -o ui` skipped only the already-completed UI build/install step in an offline environment. Verify the actual selected Go compiler.
6. Run `tests/browser/audit.py` through CI against its real Go/Vue synthetic fixture. Inspect screenshots and console. Added scenarios cover rules, maintenance/cancel, admission plus native reauth dialog, read/unread and password change. This audit's browser was blocked before login; do not reuse baseline's 14/14 as proof of v6. Do not weaken assertions.
7. Commit coherent integrated code to main, as the owner requested, and build production packages with the real resulting commit/version/signatures. This delivery contains source/build evidence, not signed production binaries.

## Important deployment changes

- Back up the complete protected controller state and test new schema/time indexes on a populated copy. Index creation may hold a write lock; bounded runtime cleanup does not make the initial migration instantaneous. A DB-only snapshot is not full protected restore.
- Update server and UI first, then compatible agents. Preserve selected controller URL, identities, secrets, trust and spool. Do not re-enroll current machines or reset to the compiled bootstrap IP.
- **Unknown automatic-profile identities are now closed by default.** Owner must open the admission window on Add machine before first arrival. Existing trusted machines, pending/approved candidates and one-use codes are unaffected by closing it. Do not diagnose the intentional `admission_closed` reply as broken networking.
- Verify global rules on a disposable machine. They are only CPU/used-RAM/most-filled-disk percentages. Keep rule history and `policy_changed` separate from observed recovery. Existing historical incidents are not rewritten as retrospectively healthy.
- Maintenance continues collection and real incident evidence. Cancel records an end, not history deletion. UI has fleet and machine creation; service-only API scope is not a finished service wizard.
- Changing owner password invalidates all browser sessions, including the current one, but not agent keys. Verify two sessions and sign-in using the new password. A lost response is an unknown result, not automatic safe retry.
- Downgrading to a pre-v6 server ignores the new admission/rule/maintenance policies. Treat rollback as a reviewed deployment/state change, not continued enforcement by an unaware binary.
- Source publication alone must not restart the owner's active controller.

## Actual evidence and remaining gates

Go race: 178 top-level tests / 239 pass events including subtests / 18 tested packages. Frontend: 57 Node tests. Vet, module verification, TypeScript, production UI and Linux/Windows cross-command builds passed. Browser syntax passed; browser execution blocked. Native systemd/SCM installation, independent supervisor recovery, power loss, fleet load/full v3 and 24h soak remain NOT RUN.

Independent service-host self-update recovery, native installer users/ACLs, immutable release catalogue/cohorts/resume, complete protected restore and migration reconciliation, long-term aggregation and measured capacity remain mandatory release work. `operation.retry_selected` and `update.resume` must not be enabled without real orchestration. Do not remove supervisor self-update guard without native independent recovery. `rule.save` and `maintenance.set` are now implemented only for their validated subset, not a generic workflow/rule engine.

Telegram, a remote shell, host reboot and unrelated application control remain excluded. Deliver actual evidence for each release gate instead of another blanket readiness claim.
