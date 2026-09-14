# Grok: integrate cumulative audit 4 from 4f85de3

Read `AUDIT_REVIEW_4_2026-09-14.md`, `CUSTOM_SERVICE_CHECKS_RU.md`, then `RELEASE_COMPLETION_PLAN.md`. This is real source, not a directive-only package. No production service was changed by this audit. No new main commit exists for this package.

## Exact integration route

Baseline commit: `4f85de31f837dbdfeb77e9f562726de5f00b9d28`.
Baseline tree: `fdec03382ddab5f3ddcfe8984e9dc57a1d2788d7`.
The package manifest records the corrected tree, source hash and patch hash.

1. Run `python3 verify_package.py` in the package directory. Use `python3 preflight.py --repo /absolute/path/to/monik_v2` for read-only integration checks. This does not modify the repository or deploy anything.
2. Work from a clean checkout/worktree. Preserve all newer main changes and the owner's uncommitted work. Do not force-reset to the baseline merely to match a hash.
3. For the exact baseline: `git apply --check /path/to/monik-audit4-from-4f85de3.patch`, then `git apply /path/to/monik-audit4-from-4f85de3.patch`. Review the entire diff. The reference source ZIP is not an instruction to overwrite a dirty repository. Do not reapply audit2 or audit3, which are already in the baseline.
4. Rebuild matched UI and server. Connected build commands:

```sh
cd web
npm ci --no-audit --no-fund
npm test
npx --no-install tsc --noEmit
npm run build
cd ..
go mod verify
go test -race -count=1 ./...
go vet ./...
make dist
```

5. Run the expanded `tests/browser/audit.py` using the repository's CI fixture and inspect actual artifacts. Audit 4 was blocked at navigation by browser policy, so do not carry forward baseline browser PASS. Test a real native agent separately from synthetic browser data.
6. Integrate to main, as requested by the owner, only after preserving concurrent work and reviewing local results. No force push. Do not remove fail-closed guards simply to get a green UI. Do not auto-deploy/restart production merely because a source commit exists.

## Server/agent rollout and data safety

Back up the full protected controller directory correctly, not only a live .db file. Preserve TLS/CA, encryption key, release trust, identity, settings and agent protected state. Verify the backup is recoverable on a disposable copy. This patch's key checks intentionally refuse corrupt/missing keys rather than generating replacements over encrypted records.

Deploy the matching server/UI first in a test environment. Additive discovery tables are created by the normal schema path. Existing persisted controller URLs win over bootstrap defaults; no re-enrollment is necessary. FULL SQLite synchronous mode changes I/O latency: measure on the actual disk and do not quietly revert the durability claim to pass a benchmark.

Update the worker and the reviewed service-host using the existing explicit supported procedure with an operator-maintained known-good copy. Supervisor self-update remains explicitly unavailable, so do NOT enable it by removing the guard. Confirm the installed worker reports `http_custom_v1` and `health_advisor_v1`. The server/UI reject new definitions until that capability report exists.

Legacy profiles with no custom request fields preserve their hashes. After v1 custom definitions are saved, DO NOT blindly roll back to an older agent that ignores fields or cannot validate the new schema. First restore a compatible legacy profile and confirm it, or preserve/recover the matching new worker. A baseline server cannot fully manage a custom profile. Test this boundary on a disposable pair before rollout.

Existing services keep their checks, including HEAD, pause, ignore, credentials and custom paths. New discovery suggestions are non-destructive. Use rediscovery -> preview recommendation -> explicit trial -> save for existing services. New services without any primary definition may get a conservative verified health template. A negative readiness endpoint must remain negative, not be swapped for positive liveness.

## Mandatory native acceptance journey

Start a disposable controller and two real agents where available. Enroll, receive capability, discover a nonstandard local port, and configure GET path/query + typed bool predicate. Observe success -> negative JSON -> HTTP503 -> stopped socket -> recovery. Verify distinct failure reasons and observation freshness.

Configure an explicitly read-only POST endpoint with static JSON/header, 30-second interval and 10-second deadline. Capture the request on that test service and verify exact bytes/headers and timing. Trial does not save, duplicate delivery does not repeat the trial, a worker crash leaves unknown outcome, slow service does not block control, and response/body secrets do not enter operation evidence/logs. Confirm pause remains paused after template edit.

Add 401, self-signed TLS, wildcard IPv6, HTML catch-all, all-paths JSON OK, empty 503 readiness with healthy liveness, and a known non-HTTP process. Auto-advice must not bypass permissions or health failures. A control route timeout/200 blocks automatic approval.

Exercise old agent capability rejection, draft preservation on refresh/tab/navigation, apply revision conflict, offline target feedback, reload operation status, and overview/history invariants. Then finish the P0 release tasks in the completion plan. Unit tests and cross-compilation do not complete these native journeys.

## Delivery back

Return coherent main commit/hash, actual commands/results, native platform matrix, tested rollback boundary, and remaining P0/P1 list. Do not label the product fully released while service-host recovery, immutable rollout, full restore or retention requirements are still incomplete. Telegram stays deferred. No remote shell, host reboot, unrelated app control, subnet scan, auth guessing or autonomous POST discovery.
