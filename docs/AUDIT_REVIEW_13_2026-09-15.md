# Monik V13: reliability audit and corrective source

Date: 2026-09-15. Exact reviewed baseline: `5fef10f15b610ff79b036f040d45037175b28866`, Git tree `82c6300e389bbd4401c60e02cf6dfeff61daa713`.

**Source patch, not a production deployment or a signed release. No GitHub write, owner endpoint, SSH session, production database, service installation or host reboot was performed.** Existing release blockers remain blockers. Numerical test results are in `audit/validation-review13-2026-09-15.json` and the delivered evidence directory; do not copy old acceptance counts to this revision.

## Review and baseline

The exact CI source artifact was extracted and its complete Git tree verified against the requested commit. The clean baseline race suite passed: 305 top-level pass events (including its fuzz target), 401 including subtests/seeds, 21 packages. Baseline CI run 34903169529/job 104173631459 also completed successfully, including its browser step. This is prior-version evidence, not V13 visual acceptance.

An initial local baseline invocation overlapped the first Vue build and failed Go embed setup because the UI output did not exist yet. That is a **test-harness ordering mistake**, not a product defect. The corrected baseline builds the UI first and passes; both attempts are retained and clearly named.

The review traced active check configuration into scheduling, summaries and incidents; control response and receipt delivery; startup journals; historical replay; queue retention and restart; controller address construction; and corresponding UI feedback. It adds no remote execution or new fleet framework.

## Confirmed before/after cases

Thirteen named regression scenarios failed against unchanged baseline behavior and pass after correction. They are overlapping failure modes, **not thirteen independently established security vulnerabilities**.

| ID | Confirmed behavior before this patch | Correction | Regression / area |
|---|---|---|---|
| R13-01 | Initialized worker could start with defaults after losing applied config files, or use an older/mismatched mirror | Missing initialized config or conflicting revision/hash stops startup without overwriting protected state. Valid newer atomic envelope and verified legacy rollback reconciliation are preserved | `TestV13AppliedConfigCannotSilentlyFallBackAfterDataLoss`; runtime |
| R13-02 | Previous-definition result could appear current/fresh or open an incident after the desired check changed | Summary requires the current applied configuration, check ID and observation revision; late old results remain history, not today's health or recovery | `TestV13OldCheckResultsCannotDescribeNewConfiguration`; server |
| R13-03 | Duplicate JSON members could make a contradictory health body pass through last-value decoding | Exact decoded duplicate member names, invalid UTF-8 bytes, excessive nesting and trailing data reject health/JSON predicates. HTTP response evidence remains distinct | `TestV13DuplicateJSONFieldsCannotManufactureHealth`; checker |
| R13-04 | A valid control-response prefix was accepted before an invalid or oversized tail | Read the entire bounded object before any config/job/ack effects | `TestV13ControlResponseMustBeCompleteBeforeAnyEffect`; runtime |
| R13-05 | Both secret retrieval paths accepted a valid JSON prefix, duplicate values or oversized tail | Both secret fetch paths use the same bounded complete-object validation; errors omit body contents | `TestV13SecretResponseRequiresOneCompleteObject`; runtime |
| R13-06 | Receipt ACK deleted in-memory replay protection even when replacement of its journal failed | Keep/restore the exact result in memory if durable receipt removal fails; no repeated job execution from that response | `TestV13ReceiptRemovalRequiresDurableJournal`; runtime |
| R13-07 | First 64 pending IDs could permanently hide later completed receipts | Fair rotation of the bounded receipt batch; cursor is ephemeral scheduling only, evidence stays durable | `TestV13PendingReceiptBatchCannotStarveCompletion`; runtime |
| R13-08 | Retrying a queued identity with changed metric contents silently retained the wrong version | Shared immutable observation serialization with server dedup; changed payload at the same record identity rejects, transport-only retry still works | `TestV13SpoolRejectsChangedPayloadForSameIdentity`; spool/protocol |
| R13-09 | A successfully reconnecting worker did not age out an expired queue head unless another upload failed | Apply existing retention on queue reads as well as writes | `TestV13HealthyDrainExpiresAgedQueueWithoutANewFailure`; spool |
| R13-10 | Restart erased the counter and interval of queue losses | Durable write-before-delete loss ledger; replay pending deletion without recounting; bounded journal batches and explicit corruption errors | `TestV13LossEvidenceSurvivesRestart`; spool |
| R13-11 | Historical telemetry with an obsolete job receipt was rejected by job ownership lookup | Backfill never applies/acks cached commands and does not depend on expired job history. Live receipt authorization remains unchanged | `TestV13HistoricalTelemetryDoesNotDependOnExpiredJobHistory`; storage |
| R13-12 | Control-only / paused reports erased observed IP addresses | No host observation preserves last known addresses; an explicit empty address observation still clears them | `TestV13ControlOnlyReportPreservesObservedAddresses`; storage |
| R13-13 | A valid controller origin ending in `/` produced `//api/v1/agent/report` | Normalize the request join only. Persisted URL, migration identity and trust are untouched | `TestV13ControllerRootSlashKeepsReportRouteAndIdentity`; runtime |

Evidence: `regressions-before.log`, `receipt-starvation-before.log`, `loss-address-before.log`, `root-url-before.log`, `secret-envelope-before.log`. First failures are deliberate regression execution, not failed final acceptance.

## Boundaries of the corrections

### Current versus historical service evidence

No old observation is deleted. If a desired configuration has not been confirmed, or the observation belongs to another revision/check, the service reports `pending` and `fresh=false`. This can make all checks of one machine briefly pending during an agent-configuration change: one revision covers the whole agent config. A fresh result of the current definition is required, not a guess based on equal-looking URLs.

Old failures also cannot reopen incidents, and old successes cannot resolve today's incident. Previously confirmed incidents are not automatically marked recovered when a check changes. The UI stops prescribing corrections from an old refusal/auth error, labels a previous response, and shows its revision/time. Baseline HTTP code-only monitoring still means HTTP responsiveness, not a JSON-health guarantee.

The browser fixture formerly omitted observation revisions even though real agents send them. It now explicitly reports its actual fixed revision 1. It does **not** pretend to apply new configs or execute jobs. The additional browser case verifies a newer desired definition stays pending against those old observations.

### JSON validation

The new small `internal/jsonutil` package retains the existing encoding/json wire representation and hashes; no dependency or global serialization migration is introduced. Object keys are compared by exact decoded spelling, including escape sequences. JSON member names remain case-sensitive. The validator has a nesting bound; control and secret readers enforce total byte limits including trailing whitespace. It does not claim schema-level rejection of every unknown field or replacement of all project parsers.

Standard JSON decoding can use later duplicate values. This audit uses explicit rejection at control, secret-response and health-evaluation boundaries instead. References: https://pkg.go.dev/encoding/json and https://www.rfc-editor.org/rfc/rfc8259 . These explain the protocol constraints; the test evidence comes from Monik itself.

### Queue loss ledger

New file: `<agent-state>/spool/loss-ledger-v1.meta`, schema 1, mode 0600. It records the cumulative count, first/last **retired record file timestamps**, and at most 256 relative retirement paths per pending batch. These bounds are not a complete per-second missing-data map. Losses already forgotten before V13 cannot be reconstructed.

The retirement decision is atomically written before unlink. A pending batch resumes at startup without counting it again. Completed removals are directory-synced on Unix before clearing the journal. Missing/invalid types, duplicate JSON fields, negative counts and path traversal reject rather than overwrite the ledger. No record is deleted when initial journal publication fails. Repeated open, interrupted deletion, chunking and snapshot mutation have dedicated tests. A separate self-review test caught a directory-symlink escape in the first draft of the new ledger; V13 now rejects symlinked spool directories and rechecks the immediate retirement parent. The failing and corrected logs are retained as `ledger-self-review-before.log` / `ledger-self-review-after.log`. This is an additional new-code review case, not one of the thirteen defects reproduced on the original baseline.

Windows directory synchronization is not claimed: that platform retains the existing atomic file strategy and still needs native power-loss acceptance. Any filesystem that rejects the required sync reports a retention error rather than claiming durability. Actual storage/controller power cuts were not tested. Old workers ignore the .meta file but cannot account for losses they cause; do not claim continuous loss accounting across a deliberate downgrade.

Existing age/size limits are unchanged. Historical telemetry still obeys server schema, identity, time, service ownership and size checks. Only cached command-receipt dependence is removed for non-live backfill.

## Executed checks and limitations

Read the accompanying machine-readable validation summary for final counts and exact exit codes. The final source is exercised with a full Go race pass, vet, module verification, frontend tests/typecheck/bundle, Linux and Windows cross-builds, repeated new regressions, and a bounded fuzz run. The existing three opt-in Linux process scenarios are rerun against newly built binaries: real host telemetry/offline queue, supervisor respawn/shutdown, and a signed worker upgrade followed by failed-candidate recovery under UID 65534.

The signed working candidate in that existing fixture is the same ELF with an inert byte trailer, producing a different digest. It proves replacement/session/signature/recovery mechanics, not arbitrary future state-schema compatibility. Loopback fixtures and direct SQLite fault injection are not production load, native service installation, a complete backup restore or a multi-machine rollout.

The local browser attempt was blocked by `ERR_BLOCKED_BY_ADMINISTRATOR` at the isolated login URL before authentication. No alternative host/browser-policy bypass was attempted. The revised script compiles and is delivered for Grok's permitted browser environment. Native Windows SCM, Linux systemd installation/boot, physical TV/Yandex, disk power loss, complete owner v3 acceptance, capacity and 24-hour soak remain NOT RUN here.

### Final measured results

| Check | Result |
|---|---|
| Full Go race | PASS: 326 top-level / 442 including subtests and seeds / 22 packages |
| Frontend | PASS: 108 Node tests, tsc --noEmit and production bundle |
| Native compiled Linux | PASS: three scenarios, UID 65534 |
| New regressions | PASS: 10 shuffled repetitions across six packages |
| JSON fuzz | PASS: 152,494 executions, 15-second requested run, two workers |
| Vet/module integrity | PASS |
| Linux/Windows amd64 | Four commands each compiled; Windows not executed natively |
| Browser | Blocked before login; NOT RUN to completion |

Use `sealed-*` evidence for final Go/native/build/repeat/fuzz counts. `final-web.log` is the final frontend pass. Earlier correction stages and the initial harness error are retained for provenance, not substituted for final results.

## Integration

Use exactly the included patch on the pinned baseline, or deliberately integrate newer main changes. Full source is a reference, not permission to overwrite a dirty worktree. Build UI before server. Deploy server/UI, then worker and service-host built from matching source. No agent re-enrollment, default endpoint reset, credential replacement, schema-number change or new privilege is required by V13.

Back up complete protected state before deployment. Do not delete config/receipt/loss journals to silence corruption errors. Keep independent SSH/provider access for the first Linux pilot; perform actual service restart and boot acceptance before broader rollout. Never downgrade a controller during an active V11+ rollout. See `V13_RELIABILITY_RU.md` and `RELEASE_COMPLETION_PLAN.md`.
