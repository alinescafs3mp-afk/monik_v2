# Monik Audit 9: durable operation read state and trustworthy results

Date: 2026-09-14. Source baseline: `efae8069f27f3ba2f951273796dbd99668db364d`, tree `0ef404f114c7b8d34ff60976d342d9c8d3540225`.

**Pre-release source correction, not a production deployment or full-system certification.** The exact GitHub Actions source was checked against the baseline tree. The owner installation was not accessed, restarted or modified. All previous integrated monitoring, TV, naming, SSH and agent behavior is retained.

## 1. Owner requirement delivered

An owner can mark any operation read or unread on its detail page or in the operation list. Multiple explicitly selected rows can be marked together. A read operation no longer contributes to red attention indicators in its row, the global header, or Overview. Its execution status, failure message, evidence, incident health, jobs, retries and cancellation state are not changed. The read/unread mutation is ordinary protected server metadata, not another operation that itself needs reading.

A new failure in a previously acknowledged operation becomes unread. A failed target inside an otherwise running multi-target operation contributes to attention. Successful progress, duplicate receipts, and recovery/removal of an already known waiting target do not re-open an unrelated acknowledged failure. New failure status, stage, code or reason does. Offline waiting and expiry remain explicitly different outcomes.

Read metadata is durable across tabs, devices and server restart for the current single-owner product. The UI shows who acknowledged the current notice and when. Repeating the same acknowledgement preserves its original time/actor. Reversal is audited. This is not a per-viewer enterprise notification system.

## 2. Concrete code corrections

| Finding | Correction and evidence |
|---|---|
| Global attention looked only at the latest 200 operations | Full-journal aggregate query and lightweight `/operations/summary`; regression has a failing operation older than 205 others. List pagination does not alter the total. |
| Read state could hide a failure arriving during a click | Dedicated attention generation and frozen `(operation_id, attention_revision)` selection. Compare-and-swap validates every target before any update; a stale selection returns 409 with no partial writes. |
| Generic operation revision changes on routine progress | Attention has an independent structural reason set. Only new or changed problems advance its generation, so ordinary progress and recovery do not cause notification noise. |
| Target state and aggregate were published in separate transactions | Target, corresponding job state where applicable, aggregate, notice generation and event publish together. Fault injection proves rollback when either aggregate or job write fails. |
| Delayed `accepted` receipt could rewind a `running` job | Persisted progress ranking ignores stale nonterminal receipts. Existing terminal/result evidence validation remains. A duplicate does not execute work again. |
| Updating a nonexistent target reported success | Affected-row count is checked; missing target returns an explicit error. |
| Broken stored operation parameters could look valid | Strict JSON/time/evidence decoding; database errors remain errors rather than 404/empty success. Request-key lookup does not treat a storage failure as permission to start a new action. |
| Pending job decoding ignored invalid JSON/identity | Malformed/null/mismatched stored envelopes fail closed before reaching the agent. Action, operation/job IDs, schema and deadline presence must agree with their stored envelope. |
| Querying dependent targets while list rows held the only DB connection could deadlock | List rows are fully read and closed before target queries in the same bounded read transaction. The one-connection regression passes. |
| Expiry and desired-configuration publication could leave summaries inconsistent | Expiry and configuration target evidence refresh attention and aggregate in their existing transaction. |
| Browser could show a stale global count after a read event during an active refresh | Coalesced follow-up summary refresh; no silent dismissal based on a client-only count. |
| Old-controller writes during a downgrade predate acknowledgement metadata | Startup reconciles operation notices in keyset batches of 500; unchanged reads survive, changed failures become unread. |

Four tests were observed failing against the baseline before fixes: missing target success, corrupt operation read, delayed receipt regression, and partial target/aggregate publication. Additional tests cover the job-write fault, rollback/restart reconciliation, bulk CAS, partial-failure aggregates, stale cursors, equal timestamps and corrupt job payloads.

## 3. API and storage contract

`GET /api/v1/operations` returns a bounded page and global counts. Filters: `all`, `running`, `attention` (unread attention), `errors` (attention including read), `completed`; read filter: `all`, `read`, `unread`; optional literal action/actor/ID search `q`. Default page 50, maximum 200. Keyset pagination orders by creation time and ID and excludes later insertions after the first-page anchor. Status/read membership is live, not a frozen historical snapshot; changing filters invalidates the cursor.

`GET /api/v1/operations/summary` returns `total`, `running`, `attention`, `unread_attention`, `read` across the journal. Counts may overlap: a multi-target running operation can also require attention. No frontend download of the latest 200 rows is needed to count them.

`POST /api/v1/operations/read` accepts exactly a boolean `read` and 1..100 unique versioned targets. Owner authentication and CSRF are required. Read acknowledgement does not require another password prompt because it grants no machine authority. Validation, update, audit and event insertion share one transaction. Missing target, stale generation and malformed inputs have distinct status codes. Lost transport/commit acknowledgement remains uncertain in the UI and triggers a reread, never automatic re-execution.

The `operation_attention` table is additive. Read state and execution revision are separate. Startup reconciliation is restartable with bounded transactions, but total work grows with the stored journal. Back up first and measure upgrade startup on very large journals. Normal row/query limits are not a proven large-fleet throughput guarantee. Standard time-series retention remains unchanged.

## 4. UI delivery

The operation list adds per-row read/unread, explicit selection, bulk actions, read filters, safe search and forward/back paging. Existing unknown original requests can still be reconciled; acknowledgement never retries those requests. Error outcomes remain inspectable through the problem filter after being read. The detail page exposes the same acknowledgement and actual target evidence.

The red indicator is a notification, not historical truth. No acknowledgement makes a failed job successful, removes a machine's problem border, deletes an incident, cancels queued work or closes SSH. Per-row and bulk requests show saving, conflict and uncertain-result feedback. The new browser scenario checks reload persistence, bulk selection, neutral read styling, lost-response reread and narrow layout on the disposable API fixture.

## 5. Executed validation and boundaries

Final measured counts and exit codes are in `docs/audit/validation-review9-2026-09-14.json` and the delivery evidence directory. Baseline race tests passed despite the four reproduced defects. The corrected suite, frontend helper/request tests, TypeScript, Vite, vet/module verification and Linux/Windows builds are executed independently. Opt-in compiled Linux worker and supervisor smoke tests use an unprivileged identity, local HTTPS, real CPU/RAM, stop/restart, failed upload and spool recovery. Existing encrypted SSH fixture tests are rerun in the race suite.

The attempted new Chromium walkthrough was **BLOCKED_BEFORE_LOGIN** with `ERR_BLOCKED_BY_ADMINISTRATOR` on the isolated loopback fixture. No browser-policy bypass was attempted. The updated scenario is included for normal local/CI execution, not presented as executed visual acceptance. Physical TV/Yandex, native systemd/Windows SCM boot, independent supervisor update recovery, fleet-scale load and a 24-hour soak remain NOT RUN here. No production credentials are used or packaged.

## 6. Remaining release priorities

Do not interpret this usability correction as completion of independent service-host self-update, immutable release publication/cohort orchestration, full-controller protected backup/restore, long-term aggregates or native Windows provisioning. `operation.retry_selected` and `update.resume` remain guarded; read state is not a substitute for either feature. Continue the existing completion plan.

Further audit targets: several legacy server-only effects still span their own state commit and result publication. Acknowledging a result cannot repair those effects; retain explicit unknown-result handling and finish their transaction/outbox boundaries. Do not globally turn storage errors into successful acknowledgement or automatically retry disruptive effects. Add measured journal-retention/dedup horizons and viewer-only wallboard credentials before shared-screen deployment.

## 7. Integration and deployment

Apply the one patch to a clean worktree based on `efae806`; preserve any newer owner changes and retest conflicts. Build UI before embedding it in the server. Source publication is not deployment. Back up the protected controller directory before upgrading; retain keys, identities and configured endpoints. This release's read metadata/API changes require the matching server and UI; no re-enrollment, changed URL, new permissions or agent configuration is required. Rebuild existing binaries normally but do not advertise these local test binaries as signed releases.

Old clients can ignore additive fields. An older server cannot serve the new read endpoint; do not mix the new UI with it. Rolling an old binary over the additive table loses read-aware UI temporarily; forward startup reconciliation rechecks any changed operation reasons instead of trusting stale acknowledgements.
