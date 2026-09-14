# Monik Audit 11: durable canaries and bounded update waves

Date: 2026-09-14. Reviewed upstream commit: `b110c2de7688d8d2d28f6c5498bb276c8825a1f8`; upstream tree: `8a6441bc287174edb750d9637cacba71eca5467a`.

**PRE-RELEASE.** This is a source correction and bounded implementation slice. The owner's installed controller and agents were not contacted or changed. Existing baseline CI results apply to that baseline, not this new source. See `audit/validation-review11-2026-09-14.json` for actual current execution evidence. No automatic rollout is triggered by applying this patch or restarting the controller.

## 1. Why this round

Audit 10 made release bytes immutable, but its complete per-agent jobs were published to every selected machine at once. The owner-requested canary/cohort/pause/resume behavior was still missing. Audit 11 implements that bounded controller-side state machine without changing the agent's privileges or replacing the v10 trust and download checks.

The scope is WORKER updates only. Independent recovery for replacing the service-host itself remains missing and guarded. General `operation.retry_selected` remains unavailable. New `update.pause` and implemented `update.resume` control an existing frozen rollout; they are not arbitrary job replay APIs.

## 2. Behavior and limits

| Contract | Implementation |
|---|---|
| Preflight | Every selected agent must pass release/platform/managed/capability/lifecycle checks before any rollout job is released. An invalid member rejects this entire new rollout, with individual reasons. |
| Freeze | The release ID, exact signed target, target IDs, platform, job IDs and wave membership are durable. New members of a group never join an already started rollout. |
| Canary | One deterministic agent per OS/architecture, sorted by platform and stable agent ID. Canary waves execute separately and sequentially. The UI previews their human-readable names before submission. |
| Standard wave | Default 2 machines; integer 1..10 allowed. At most one wave per rollout is released at a time. |
| Observation | Default 30 seconds; integer 15..300 allowed. All current-wave workers must first pass existing update receipt validation. Their current digest and process session must match their pinned job/receipt and contact must be fresh. Previously confirmed earlier canaries are checked too while later waves are pending. |
| Missing evidence | A failed, rejected, unsupported, rolled-back, expired or unknown target blocks new dispatch. A previously confirmed worker losing its identity or fresh contact also blocks. Merely reading the operation never unblocks it. |
| Completion | Even the last wave must finish its observation period. An all-succeeded target list during observation does not prematurely finish the parent operation. |
| Limits | Maximum 500 frozen targets and 16 running/paused/cancelling plans; recent management list 30 plans, with older details accessible by operation ID. These are limits, not measured capacity claims. |
| Time | Plan authorization lasts 24 hours. Released jobs get at most the existing ten-minute one-shot lifetime, clipped to signed metadata expiry and the plan deadline. Pausing does not extend any lifetime. |
| Restart | Policy, progress, jobs, results and pause are persisted. Controller startup resets an in-progress observation timer. A timer gap greater than 15 seconds or backwards movement also restarts observation; downtime is not counted as proof of stability. |

The observation criterion is **updated worker identity and fresh control contact**, NOT a configurable application-health/SLO gate and not proof every monitored service is working. Fresh contact uses controller receipt time; historical spool traffic alone is not proof. Native fleet installation and power-loss acceptance remain separate requirements.

## 3. Pause, failure and cancellation

A pause prevents claiming any new released-but-unclaimed job and holds later waves. A claimed job is already possibly delivered, even if the HTTP response is lost. It may finish, and the same envelope may be retransmitted until acknowledged. This deliberately does not promise network-wide instantaneous recall.

Resume is allowed only from an owner-paused plan, at the expected rollout revision, after recent authentication and expiry/failure checks. It retains the same frozen jobs and resets observation. Automatic `blocked` is not resumable through this button: cancel unstarted work, inspect actual results, then submit a deliberately reviewed new selection. A still-active/unknown lifecycle blocks unsafe overlap. A new failure after a pause was marked read becomes new attention.

`operation.cancel_pending` cancels preparing/held/unclaimed jobs and changes the rollout to cancelling. It does not label already claimed work as never executed. The rollout becomes cancelled when all remaining results are terminal, retaining failures and rollback evidence. A claimed update that exceeds its result deadline becomes `unknown_result`, not a false claim that nothing executed. It keeps the lifecycle conflict boundary until actual reconciliation.

## 4. Confirmed baseline defects and fixes

Three new regression tests were actually run against the unmodified behavior and failed, then pass with the corrections (`regressions-before.log` in the delivery evidence):

1. A receipt for an update still in internal `preparing` could promote it to running. Receipts for unpublished `preparing` and `rollout_held` work are now rejected.
2. `PendingJobs` returned a job before `JobEnvelope.NotBefore`. The query decoder now honors that time and the signed/queued job's actual deadline.
3. `HasActiveLifecycle` treated a closed/erroring database as no conflict. A non-not-found error now fails closed, reporting unavailable lifecycle state instead of admitting overlapping work.

Adjacent code fixes covered by new tests: atomic full-scope wave publication; durable dispatch claim versus pause/cancel race; no partial control-state update if recording its outcome fails; schema/API parameter validation; expired delivered effect classified unknown; one-connection rollout reads; bad stored expiry cannot resume; target JSON does not expose internal envelopes; new blocked evidence invalidates an old read marker.

The first full new-source run also caught an OLD test that still expected `update.resume` to be unimplemented. That obsolete assertion was removed only for resume and replaced with tests of recent-auth enforcement, invalid request rejection, CAS, same-job continuation and transaction rollback. The still-unimplemented generic retry assertion remains. The failing intermediate log is retained as development evidence, not counted as a final pass.

## 5. Transactions and dispatch boundary

New tables `update_rollouts` and `update_rollout_members` reference the existing operations/targets/jobs/catalogue. Publication inserts every frozen member, pins every envelope and authorizes the initial canary within one transaction. Individual wave release prechecks all its members before changing any of them.

The HTTP control endpoint now uses `ClaimPendingJobs`: selecting eligible jobs and persisting their delivery claim happen in one transaction. Pause/cancel transactions therefore cannot slip between selection and claim and falsely classify possibly delivered work as unsent. Inspection-only `PendingJobs` is retained for existing tests/non-dispatch readers. Parent execution status, notice and event are recomputed transactionally. UI detail reads return state/revision/members from one database snapshot.

The controller scheduler ticks every five seconds and is also evaluated before job reads. It skips restore mode; restore mode now does not issue new job claims. This is an additional boundary, **not implementation of complete restored-config/job reconciliation**. Existing desired configuration delivery and broader restore requirements still need separate review before accepting a full restore.

No new dependencies, database server or message broker were introduced. Existing SQLite storage remains the authority. Transactions serialize writes and hide uncommitted state from other ordinary connections; those properties do not magically extend to already transmitted network messages or OS processes [R1].

## 6. UI

Updates now has batch-size and observation settings and a separate pre-submit review. Each persisted rollout shows canary/regular wave, confirmed/held/failed/cancelled counts, actual observation start, reason, per-target results and pause/resume controls. The same component appears in Operation details. Targets are loaded in the bounded DTO; their table is collapsed until requested. Closing/reloading the page does not cancel work.

Accepted requests, rejected preflight and committed control results have different messages. Stale control revisions reject; no fake percentages are shown. Controls remain within wrapping containers, and the browser scenario includes a 390px viewport assertion. Current machine/service selection, TV layout, names, monitoring settings, read markers and SSH console are retained.

## 7. Verification boundaries

Actual final counts and exit codes are produced from current logs in the machine-readable summary. Full Go race, frontend helper tests, vet, module verification, typecheck, production build and Linux/Windows cross-command builds are executed. The two opt-in built Linux process tests are run separately under UID 65534, with real CPU/RAM, stop/restart/child respawn and rejected upload/spool recovery.

New tests use real temporary SQLite, authenticated handler paths and existing locally signed release fixtures. Wave results/contact changes are controlled test evidence, not claims of a real remote fleet update. Ten repetitions exercise claim/pause, same-ID resume, publication faults and restart behavior. Existing encrypted SSH fixtures run in the full Go suite, not as a native shell walkthrough.

The expanded real-UI browser test was attempted on the local HTTPS fixture. Chromium was blocked with `ERR_BLOCKED_BY_ADMINISTRATOR` before login. No browser checks or new screenshots are counted as passed. The authored scenario adds a signed NON-executable fixture bundle and synthetic no-credential agents to test preview/pause/reload/resume/cancel without executing any update or forging success. Its syntax is checked; the integrator must actually execute it in an allowed environment.

NOT RUN: native systemd/Windows SCM boot and installation, actual signed multi-machine upgrades, network partition/power cut recovery, physical TV/Yandex, long-retention/load and 24h soak. NOT IMPLEMENTED: independent service-host recovery, protected full restore and full application-based rollout health policy.

## 8. Integration and downgrade cautions

1. Apply the SINGLE patch only to the exact baseline or consciously integrate concurrent changes in a clean worktree. Preserve runtime state; do not reset owner main or copy the snapshot over a dirty tree.
2. Back up protected controller state using the existing careful procedure; a DB-only snapshot is not a full recovery package. Test the additive migration on a populated copy.
3. Build the matching UI before the server, execute all regressions/browser tests, then deploy the controller/UI together. Agents already supporting v10 `immutable_release_v1` do not need re-enrollment or a new protocol just for controller-side batching.
4. Older unbatched rollouts are NOT converted or restarted. Finish or cancel their pending work before using the new scheduler.
5. Do not downgrade the controller during an active v11 rollout. An older controller does not understand the held/wave/pause contract and could handle already queued work differently. Finish/cancel plans and reconcile delivered effects first; never restore an old DB to erase the problem.
6. A pause is not a revocation of cached work, and a longer observation is not a longer job authorization. A paused plan can expire and become blocked. Review offline canaries before starting; stable deterministic selection does not guess which machine the owner prefers.

## 9. Next work, not hidden in this completion claim

Prioritize protected full-state backup/restore with newer-agent reconciliation and independent native service-host recovery, then native signed rollout acceptance. Retain immutable release quotas/GC reference tracking, root rotation, download/control fairness and real transfer progress; configurable application-health gates; explicit chosen canaries; controlled resumption after diagnosis; generic linked retry; read-only TV authentication and long-term aggregates. None is silently enabled by this slice.

## Primary references

R1: SQLite isolation and transaction visibility, https://www.sqlite.org/isolation.html (accessed 2026-09-14).
R2: TUF roles/metadata and expiry, https://theupdateframework.io/docs/metadata/ (accessed 2026-09-14).

R2 supports keeping signed expiry as a boundary during release and resume; TUF metadata authentication is not a native installation or application-health guarantee. Implementation counts and behaviors above come from this source and its test logs, not certification by either reference.
