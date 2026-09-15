# Monik Audit 19: shared TV presentation with conflict-safe synchronization

Reviewed commit: `776076273d677046b3a765e30d781f37b44957d5`.
Reviewed tree: `61f3e23e62937654f65273aabb4f9ca53e288f4f`.
Date: 2026-09-15. No repository writes, deployment, production credentials or production network access.

## Scope and user contract

The owner can choose TV mode on a computer, adjust the overview, and have already-open TV-mode browsers follow without reloading or manipulating the TV remote. The current implementation stores column widths and TV density in each browser; those independent stores cannot synchronize devices. V19 moves only the TV layout into a small controller-owned profile. It does not make display-mode selection global.

Shared: five preferred column widths (bounded rem values), density (10/12/14), and autoplay enablement. The services track consumes the remaining width. Each screen still computes its own actual geometry, mobile fallback and row count. Width equality in physical pixels or equal rows on different viewports is not promised.

Not shared: ordinary/compact widths, selection of ordinary/compact/TV mode, fullscreen, sidebar, current page/search, expanded service lists, drafts of checks, passwords, cookies, SSH targets/credentials, terminal tickets/input, agent configuration, monitoring or pin selection. Machines/services selected for the overview remain their existing separate server-backed setting.

## Persistence and transaction contract

`GET /api/v1/display/tv` returns a controller-bound profile and server time. Missing storage returns revision zero without writing anything. `POST /api/v1/display/tv` accepts only `{expected_revision, request_id, value}`. The value has an exact versioned schema, finite bounded numeric widths, enumerated density and boolean autoplay. Canonical names, missing/null fields, duplicates, trailing JSON and oversized requests are rejected. Both routes are authenticated and no-store; writes also require owner role, existing CSRF, same HTTPS origin when present and a per-user rate limit.

The bounded profile is stored under `settings.display_tv_v1`. No schema migration, operation, job or new agent capability is introduced. CAS, the audit event and the `display` SSE event commit in a single SQLite transaction. An injected failure in either event table leaves the profile and revision unchanged. Replaying the most recent identical request is idempotent; changing its contents or replaying a superseded base cannot overwrite a newer profile. Damaged persisted data is an explicit error, not a request to recreate defaults.

## Browser lifecycle and conflict handling

A browser reads before it may save. Mount, polling, reconnect and visibility changes never publish local cache. Before the first profile exists, previous local widths/density remain a draft; an explicit publication or completed user edit creates the shared profile. Autoplay starts false for this initial draft.

Successful completed drags, keyboard changes, remote-control Apply, density changes and autoplay changes save the shared profile. Movement previews stay local until commit; reset changes widths only. During an active edit, remote revisions are retained but do not replace the current preview or steal focus. Committing that stale preview produces an explicit conflict. The owner can discard it and load the common profile. No background last-write-wins loop occurs.

SSE `display` uses a dedicated refresh hint. It does not trigger the general telemetry refresh event. A five-second visible-page fallback rereads the same small profile when SSE is unavailable; reopening a hidden tab or restoring connectivity also rereads. Network failures retain last visible values and display their unsynchronized state. None of these reads replay a POST.

If a save response is lost, a GET checks the exact request ID, expected next revision and contents. The browser never blindly repeats the mutation. A newer remote revision is a conflict; an unconfirmed result stays unknown until explicitly reconciled/discarded. Older in-flight reads cannot rewind newer accepted state. Logout/controller changes invalidate old requests by an epoch. One HTTP write remains in-flight until it actually settles, even if an earlier SSE/read has confirmed its durable result.

## Reproduced self-review defects and fixes

These defects were in the first V19 draft, not falsely attributed to the reviewed V18 production base:

1. A GET triggered by SSE could confirm the save before the original POST resolved, prematurely re-enabling a second editor. `v19-self-review-before.log` reproduces this. The corrected state tracks in-flight transport separately from confirmed profile state. The same test now passes.
2. An immediate watcher in the initial AppShell draft ran before the local props binding was initialized. Vite/tsc compilation did not detect this runtime issue. A new test compiles the real SFC and executes its setup using actual Vue lifecycle hooks; it recorded `Cannot access 'props' before initialization`. The binding order is fixed, and initial load, role change and logout pass.

The existing column composable is additionally executed under real Vue lifecycles with an injected shared backend. Remote updates do not persist, active gestures are not replaced, release publishes once, and cancellation/unmount never publish a draft. Only browser dimensions and pointer capture are mocked in these tests; they are not visual evidence.

## Security and audit boundaries

The new path contains no terminal credentials, shell commands, arbitrary CSS, target URLs or file paths. It grants no OS permission. Existing agent-console opt-in, same-origin/ticket/session checks, separate console UID and direct SSH remain unchanged. Generic retry and service-host self-update guards remain intact.

Backend tests exercise viewer/operator denial, wrong and null Origin, CSRF rejection, revoked-session reads, payload validation, bounded writes, corrupt persisted state, concurrent screens, restart and injected SQLite failures. A real local TLS server with SQLite and two independently authenticated sessions verifies save -> SSE -> read on the second session. This is stronger than two helper objects talking in memory, but it is not a physical TV test.

The broad existing Go and frontend regression suites are rerun, including console transport/security and update lifecycle coverage. This is a targeted code audit plus regression validation, not a claim that every source line or every security boundary in the full product was exhaustively reviewed.

## Validation and explicit limits

Authoritative commands, counts, exit statuses and limitations are in the delivery `evidence/validation-summary.json`. Source dependencies and lockfiles are unchanged. The installed toolchain used for Go is 1.27.0; offline dependency snapshots were used, and Go module integrity was checked. Frontend tests include helper tests, actual Vue lifecycle/SFC setup execution and narrow source guards. `tsc --noEmit` is not a separate vue-tsc check of all templates.

An ordinary run of the existing Playwright harness against the compiled local fixture was attempted and stopped at `ERR_BLOCKED_BY_ADMINISTRATOR` before login. No workaround was attempted. Therefore zero browser scenarios passed here. New V19 browser scenarios are syntax-checked and included for Grok: two independent cookie contexts, live shared geometry, density/autoplay, reload, lost-save acknowledgement, fallback without SSE, simultaneous edits, and local mode/narrow-screen isolation. Existing V17/V18 tests now wait for actual shared-save acknowledgement rather than assuming immediate localStorage persistence. Their geometry/focus assertions are not removed.

No physical TV/Yandex, native systemd/SCM boot, production console login, real remote agents, power-cut, large fleet, full restore or 24-hour soak is claimed. Native process test results, when recorded in the delivery, concern separately compiled loopback fixture processes, not OS service installation or boot.

## Integration

Apply one patch to the reviewed tree, preserving any newer changes if present. Rebuild server and embedded UI together, run `scripts/verify-audit19.sh --with-browser` in a permitted environment, then publish the reviewed build. Existing agents require no re-enrollment, changed rights or binary replacement for this feature. Keep future-download installer templates aligned with the new controller build as required by the existing installer contract.

Reload pre-V19 browser tabs once after deployment. On the computer choose TV mode and explicitly publish/edit the common profile; open TV mode on the television with its usual authenticated session. Future profile changes flow without page reload. Do not copy passwords, tickets or private browser storage to achieve this. Preserve the database as part of the ordinary protected controller state; the shared profile does not constitute a backup/restore implementation.
