# Monik: cumulative source audit 3

Prepared for the owner on 2026-09-14. Remote baseline: `2f63c0b59498beb9436c092554cfef10e1942489`, tree `c1ac8f958dca5ad11819bc0d5e7e97244ed5198b`. Audit 2 input tree: `0d15c9f09840fbf6f20d450dafd97c7fc9305d40`.

**This is a cumulative corrective source package, not an accepted release.** It contains all audit 2 source changes plus the additional fixes below. Main was read and remained at the baseline. No repository write, production login, deployment, machine restart or public-endpoint probe was performed in this pass. Earlier failed publication attempts were not retried through another channel. Synthetic test fixtures and disposable local processes are not the owner's fleet.

## 1. Method and evidence boundaries

The exact remote source archive was unpacked and its Git tree verified. Audit 2's patch was applied and reproduced its recorded tree before further work. Baseline Go race tests and frontend tests were rerun. New regressions were introduced before the relevant fixes for configuration confirmation, enrollment collisions/retries, trailing JSON, and equal-time spool records. Those tests demonstrated failures, not merely theoretical risks. Tests were then rerun on the corrected source.

Review covered enrollment/trust bootstrap, operation dispatch and receipts, ingestion, configuration revisions, spool storage, query/export paths, authentication handling, overview/history interactions, and re-read the native updater/installer, storage retention and recovery boundaries. Audit 2's TUF/rebind/collector/UI fixes are retained. This does not claim exhaustive proof of every runtime path, absence of every vulnerability, full v3 acceptance, or native Windows certification.

## 2. Preserved from audit 2

- Dense horizontal Overview rows, optional cards, CPU/RAM/disk/ping plus concise service outcomes.
- Machines' persistent Show in overview checkbox. Pinning is not pausing or ignoring monitoring.
- Remembered collapsible desktop sidebar and separate narrow-screen navigation.
- Permanently visible chart axes, time/value graduations, units, six 1/2/3/6/12/24-hour presets, min/max and gaps.
- Bounded HTTP response metadata and narrowly recognized self-reported health fields without full-body logging. A self-reported healthy word is not a configured application readiness guarantee.
- Atomic ingestion, observation identity/deduplication, current versus replayed history, durable job receipts, config CAS, signed target linkage, safer worker activation and bounded rebind trial.
- Explicit refusal of incomplete service-host self-update, custom-rule application, maintenance, selected-operation retry and update resume. These remain release blockers, not completed features.

## 3. New confirmed defects and fixes

| ID | Defect / consequence | Corrected path and evidence |
|---|---|---|
| A3-01 | Desired config changed on the agent row without a matching `config_revisions` row. Confirmation through `SetApplied` therefore could fail indefinitely. | `internal/server/operations.go`: append revision/hash/body inside the same config publication transaction. `TestAudit3ConfigPublicationCanBeConfirmed` failed before and passes after. |
| A3-02 | Enrollment consumed the one-use code before identity insertion completed. A collision or write failure could burn the code without registering the device. | `internal/storage/enrollment.go`, `server/ingest.go`: bind code, identity, verifier and first revision transactionally. `TestAudit3EnrollmentConflictDoesNotConsumeCode`. |
| A3-03 | After a lost enrollment response the client could lose the only credential/identity needed for recovery. | New client persists a random proof and identity before the exchange; server accepts only a matching proof/code/identity retry while the code is still valid. `TestAudit3EnrollmentRetryUsesSameIdentityAndProof`; TLS client retry integration test. |
| A3-04 | Enrollment replaced the explicitly selected working controller URL with the server's advertised URL, potentially routing the new agent to a stale default. The setup client also lacked a bounded redirect policy. | `agent/setup/enroll.go`: retain the selected origin, strict TLS, 15-second deadline, no redirects, bounded response, scoped trust, no overwrite of an existing enrollment. Enroll response does not claim a config was applied. |
| A3-05 | The decoder accepted a valid JSON prefix followed by an extra document. LimitReader alone could leave trailing bytes unnoticed. | `server/json.go`: bounded full-object decoding rejects oversized, trailing, malformed, array/null requests. It does not claim duplicate-key or complete unknown-field rejection. `TestAudit3StrictRequestRejectsTrailingJSON`. |
| A3-06 | Server-only targets started as succeeded before the actual side effect. An interrupted operation could therefore contain premature success evidence. | Start them as accepted and let the specific handler persist its result. A SQLite trigger regression rejects premature succeeded target insertion. |
| A3-07 | Host spool files were identified only by observation timestamp, so two distinct reports at the same time overwrote each other. Removal likewise used timestamp rather than transport identity. | `agent/spool`: identity-derived immutable names, exact acknowledgement matching, legacy reads, bounded record reading and visible corruption errors. Equal-time regression failed before and passes after. See the explicit rollback format caveat below. |
| A3-08 | Login limiter keys could grow without a bound. Reauthentication lacked a corresponding rate limit. | Bounded 4,096 active key buckets, expired-key cleanup and refusal at capacity rather than eviction of active limits. Reauthentication limit added. Real proxy/client-IP policy still needs deployment testing. |
| A3-09 | Managed readiness could remain true after a later report said false; session expiry at the exact boundary was ambiguous. | Current readiness is updated in both directions; sessions expire at the boundary. Unknown API paths return JSON 404 instead of HTML 200. |
| A3-10 | Incident acknowledgement could report success for an ID that did not exist. | Require an updated row. An acknowledgement still is not observed recovery. |
| A3-11 | Network errors while reading the session were treated like authentication failure, sending the browser to login. A filter change during an in-flight load could be lost. | `App.vue` distinguishes actual 401 from network/server failure and provides a retry panel; coalesced refresh queue schedules the latest requested load without overlapping requests. Unit tests cover refresh/disposal and error classification, not visual interaction. |

## 4. Useful new implemented functionality

### Historical incident search

`GET /api/v1/incidents?from=...&to=...` now supports all/open/pending/confirmed/resolved/interrupted state, severity, metric, exact entity ID, bounded limits and a stable `(opened_at,id)` cursor. Results overlap the requested half-open interval, so an incident beginning before the interval but still active in it is included. Database errors are not an empty success.

The Problems page includes the six shared ranges, LIVE/HISTORY, fixed end time with visible timezone, filters, read feedback, pagination and start/resolution times. Historical views use current received evidence. Filtering by resolved/open describes the stored incident lifecycle now, not an exact snapshot of what an operator knew in the past. Full bitemporal history and historical rule/version materialization remain unimplemented.

Tests cover interval boundaries, ties, multi-page pagination and overlapping old incidents. No claim of a fleet-scale query benchmark is made; long-history indexes and maintenance budgets must still be measured.

### Genuine bounded history export

`history.export` is now implemented and removed from the fail-closed action list. It exports actual retained host samples or service observations for exactly one selected entity and `[from,to)`, up to 24 hours. The first UI entry is on machine details, with a verified link also in operation details. The API supports service data too; a dedicated service export widget is not claimed.

The export is JSON with observed and received timestamps, original bounded telemetry payloads, units, requested bounds, precision and a coverage disclaimer. Limits: 20,000 observations, approximately 12 MiB source rows, 16 MiB finished JSON, 64 MiB temporary export directory, 24-hour download validity. Hitting a limit is an explicit failure, never a successful truncated file. Shorten the interval. Missing/expired measurements are not fabricated and a 24-hour requested range is not proof of complete coverage.

The artifact is written before operation completion, downloaded only through authenticated access by its creator or owner, and checked against a stored digest. Expiry cleanup runs when creating a new export; expired files are inaccessible but may remain until that cleanup. This is a temporary diagnostic export, NOT a controller backup, native report bundle, CSV exporter or full-retention archive.

Tests check actual historical values, interval exclusion, over-budget refusal, cancellation and denied access from a different user. Browser click/download checks are added to the script but remain NOT RUN here.

## 5. Enrollment compatibility and recoverability

1. Deploy the new server before enrolling a new audit-3 agent. The new agent requires its persisted proof to be returned consistently; older servers that ignore it are rejected rather than silently accepted. Existing enrolled agents use their unchanged IDs/credentials.
2. An existing config is never overwritten by ordinary setup. A partial first attempt leaves `enrollment-intent.json` (0600) for retry with the same profile.
3. Retries are allowed only within the original short code lifetime, with both the original code and proof. This is not a permanent shared fleet token and does not weaken revocation. An expired/lost pending profile requires explicit recovery/re-enrollment, not guessed adoption.
4. Do not delete an uncertain registration's state before checking the controller. No cross-process installer locking is claimed; simultaneous setup attempts on one directory remain part of native acceptance.
5. Controller origins must be HTTPS without a query, fragment or path prefix. Reverse proxies must expose the current root-based `/api/...` paths; path-prefix deployments are not silently normalized into success.
6. The built-in bootstrap value remains the owner's original `https://46.120.103.61:8777`. An existing selected/persisted address, including the later deployed address, takes precedence and is not overwritten by this package.

## 6. Spool format and rollback caveat

New reports live in `spool/records-v2/` with filenames derived from observation time and the agent/session/sequence identity. The current worker reads both that directory and legacy timestamp-only files, and only deletes a matching acknowledged report. Duplicate retries keep the original data.

An older worker does not understand the v2 subdirectory and leaves it untouched. It can continue writing/draining its own legacy root files; upgrading again makes retained v2 reports available. It cannot immediately drain the v2 backlog. Do not certify rollback compatibility until this transition is tested with real supported versions. If an older worker is rolled back while v2 data remains, reserve disk for both bounded queues, and do not delete the v2 directory just to quiet diagnostics. The current worker enforces its total bound over both formats.

Spool loss counters remain per-process rather than a fully durable loss ledger, and aging uses write time. These limitations are explicit completion tasks. Corrupt/unreadable records are reported and retained under the size/age policy; they are not counted as delivered. They do not silently overwrite another sequence's observation.

## 7. Unfixed P0/P1 issues discovered or reconfirmed

- Full native service-host self-update still has no independent recovery mechanism. It remains explicitly rejected. Windows service identities/ACLs, Linux ownership and boot/restart/crash evidence are not accepted.
- The Windows file-control transport uses a fixed request/response pathname without per-request correlation; concurrent calls or late responses need request IDs, serialization, limits and native regression tests. Unix-socket authority, message/connection bounds and state ownership also require native hardening. Do not turn this into remote shell access.
- Release metadata and targets are still shared mutable publications. Concurrent imports, immutable per-release selection, rollout pause/resume, root rotation and full crash/rollback state compatibility need implementation and failure injection.
- Custom rule and maintenance storage are not connected to the active evaluator. Their write actions stay unavailable. Notification suppression must not erase observations; Telegram remains deferred.
- Raw retention uses large delete operations and there is no proven long-term aggregation pipeline. Event/session/operation/dedup growth needs explicit retention horizons and bounded cleanup. Dropping dedup/job receipts too early would resurrect actions; cleanup must respect replay windows.
- Backup/restore CLI is database-centric and does not transfer all controller identity/TLS/secret/update-trust material. A restored-mode flag does not implement safe job reconciliation or single-writer fencing. Do not use it as a complete migration procedure.
- Enrollment, credential/trust rotation and scheduled rebind need cold-offline, expiry, time uncertainty, restored-old-server and cancellation evidence. An unprepared offline agent cannot discover an unknown replacement IP.
- Collector/API work, slow probes, large release downloads and retention/export jobs need measured isolation from control/heartbeat freshness. Metrics should report per-collector missingness rather than a zero or vague global state.
- First-run UX, recent-auth prompts, credential change/recovery, diagnostic redaction, per-row actions, actual browser rendering and locale/date edge cases need full native/browser acceptance.

The accompanying `RELEASE_COMPLETION_PLAN.md` translates these into scoped implementation work and acceptance evidence. More cosmetic pages, AI dependencies, a plugin marketplace, Redis or a remote command console are not a substitute.

## 8. Validation interpretation

Exact commands, exit codes, tool versions, test counts and timing are in `docs/audit/validation-review3-2026-09-14.json` and the delivered `validation/` logs. This report intentionally does not copy older test counts as current results. Earlier audit reports remain historical context.

The current run uses the original locked dependency snapshot, Go 1.27.0 and Node 22.16.0. Runtime timestamps in logs report September 13; this report is labelled September 14 according to the session date. Logs are not rewritten to conceal that difference.

Browser administrative restrictions previously blocked the local fixture. This pass does not try to bypass or retest that policy. Updated browser scenarios require actual execution after integration. No native Windows boot, full OS update recovery, real fleet capacity, 24-hour soak or production acceptance is claimed.

## 9. Primary references

Technical constraints were cross-checked against Go net/http client behavior (`https://pkg.go.dev/net/http#Client`), SQLite transactions (`https://sqlite.org/lang_transaction.html`) and OWASP authentication guidance (`https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html`). They inform the design, not certify Monik. TUF/disclosure references from audit 2 remain relevant to unchanged fixes. Product limits and all pass/fail claims come from this implementation and its recorded tests.
