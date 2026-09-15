# V12 stabilization status (2026-09-15)

Read `AUDIT_REVIEW_12_2026-09-15.md` and `V12_DEPLOYMENT_GATE_RU.md` first. V12 verifies existing functions and fixes lifecycle/corruption defects. Worker signed delivery + supervised upgrade + failed-candidate rollback have a real local non-root process test. The test first exposed repeated self-upgrade, then passed after durable pending-intent reservation.

Native systemd/Windows SCM installation and boot, independent service-host self-update recovery, complete protected restore/reconciliation, large-fleet/long-history capacity and 24h soak remain OPEN. Do not use the prior historical text to claim these are done. V10 immutable publication and V11 persisted canaries/parties/pause/resume remain implemented; they were not removed by this audit. Generic selected retry remains unavailable.

First deploy matched server/UI and worker/service-host to one Linux pilot, with complete protected backup and independent access; run the local gates before the rest of the fleet. Local crash-released process locks do not fence two cloned VM controllers. No production system was changed by the auditor.

---

## Prior completion plan (historical context below)

# Completion plan after Audit 11

Baseline `b110c2de7688d8d2d28f6c5498bb276c8825a1f8`. The bounded WORKER canary/batch/pause/resume slice is now implemented and tested as specified in `AUDIT_REVIEW_11_2026-09-14.md`. Do not treat older text below saying all cohort behavior or `update.resume` is unimplemented as the current state. Generic `operation.retry_selected` remains guarded. Independent service-host replacement remains guarded.

Current completed bounds: one deterministic sequential canary per platform; frozen membership; 1..10-sized later waves; 15..300-second worker-identity/fresh-contact observation; durable pause with no recall of claimed jobs; same-job resume only from manual pause; automatic block on failed/unknown results; pending cancellation; versioned controls and restart-safe observation. This does not implement application/SLO gates or native fleet recovery.

## Next completion order

1. **Protected full-state recovery:** backup/restore of identity, trust, encryption keys, config, DB and referenced immutable releases; validate clean restore; reconcile newer agents and held/claimed/completed rollout jobs without replay. Never downgrade the controller during a live v11 plan.
2. **Independent service-host recovery:** actual systemd/SCM installation, rights, boot and failure-boundary rollback under an independent native mechanism. Existing worker process tests do not prove this.
3. **Real fleet rollout acceptance:** exact signed executable release on disposable Linux/Windows machines; canary failure, short/long controller outage, interrupted transfer/activation, manual pause then resume, offline expiry and unknown effects. Do not execute the non-executable browser fixture as a release.
4. **Remaining release controls:** quotas/GC respecting queued and rollback references, root/key rotation, protected trust recovery, download/control fairness and real byte progress. Optional explicit canary choice and application-health gates require documented criteria. An operator override must not erase a failed/unknown result.
5. **Long-term observability:** aggregates/coverage/version anchors, measured storage/load, backup/retention fairness, viewer-only TV credentials. Keep working request editor, selection, red-frame priority, names, read markers, enrollment and separate SSH gateway intact.

Audit 11 does not add a blanket generic retry, force-resume failed waves, new agent shell permissions, production deployment, TLS bypass or mandatory external services. The state machine is bounded, but its full-fleet capacity is not measured here. Read the current validation ledger before release claims.

---

## Earlier completion context (new status above takes precedence)

# Completion plan after Audit 10

Baseline ce602d0. The immutable release publication slice is implemented: verified separate object directories, transactional catalogue/trust/outcome, authenticated release-specific reads, pinned worker download and complete-job publication. Prior gate 2 language calling this wholly unimplemented is superseded by `AUDIT_REVIEW_10_2026-09-14.md`; the rest of that gate remains mandatory.

Next practical priorities: (1) independently recoverable native service-host replacement and actual systemd/SCM tests, (2) full protected backup/restore with reconciliation of newer agents, (3) persisted canary/batch/pause/resume/retry orchestration. None is replaced by successful publication. Keep existing functioning monitoring/UI stable.

Before first fleet use of this slice, perform the one-time immutable_release_v1 agent upgrade, re-import valid bundles and test actual installation/rollback of one remote test host. Retain old published files and old agent identities. Audit 10 only tests signed download of fixture bytes, not execution of those fixture bytes as a real release.

Remaining release-specific work: trusted root/key rotation; missing-key recovery without silent regeneration; full sustained download/control fairness and byte progress; bounded catalogue/orphan retention with active-job/reference accounting; recovery from expiry after long offline periods; crash/power loss between directory publication and database commit; deliberate rollback between older controller schemas. Never delete referenced objects or reset high-water to make a rollout green.

---

## Prior requirements (historical context, new status above takes precedence)

# Monik completion plan after Audit 9

Audit 9 baseline: `efae8069f27f3ba2f951273796dbd99668db364d`. The owner-requested operation read workflow is now implemented across storage/API/UI: single and selected batch acknowledgement, server persistence, audit, new-attention generations, untruncated counts and searchable pages. All current tests and limitations are in the Audit 9 report. Earlier requirements below remain authoritative where not superseded by an implemented, tested change.

## Preserve the Audit 9 boundary

A read operation keeps its real failure, target evidence and remote effects unchanged. Never satisfy an attention-clearing request by rewriting completed_with_errors to completed or deleting jobs. New substantive failures become unread. Successful progress, duplicate receipts and resolved waiting targets do not create repetitive notices. Read metadata must survive controller restart and reconcile writes by a temporarily older controller.

Keep current target/job/aggregate/notice/event changes in one transaction. Complete other legacy server-effect/result publication boundaries using explicit unknown-result handling or a durable outbox before permitting blind retry. Publish queue corruption as a diagnostic, not an empty successful control exchange. Preserve literal search, bounded paging and full-journal counts; measure larger journals and compatible retention/dedup horizons rather than reinstating a silent 200-row cap.

Do not expand into another platform while P0 lifecycle/restore tasks below remain. Preserve the optional fixed-target SSH gateway without turning the monitoring agent into a shell. Acknowledgement needs no SSH connection and adds no machine privileges.

---

## Prior completion plan retained

# Monik: completion plan after Audit 8

Authority: owner v3 requirements, later requested UX/custom-check/agent-arrival behavior and actual integrated code. Current integration baseline `3873c9c` (application `4e0ae20`); Audit 8 adds the source corrections documented in `AUDIT_REVIEW_8_2026-09-14.md`. This is a completion directive, not evidence that the remaining tasks are done.

## What is no longer an empty control

Global CPU/RAM/disk threshold editing is wired to evaluation with rule versions, persistence, hysteresis and policy-change evidence. Maintenance windows/cancellation preserve observations and history. Admission can be opened/closed with expiry and owner approval. Incident read/unread and critical escalation are meaningful. Recent-auth UI and password rotation preserve operation/session semantics. These must stay working; do not replace them with unused settings during a refactor.

These implementations are intentionally small: three global percent rules, no repeating maintenance or service-specific UI wizard, no anonymous agent auto-approval, no general RBAC, no complete historical recomputation. Baseline compact overview, pinning, grouped services, names, axis labels and custom requests remain acceptance requirements.

## Preserve the newly completed slice

Do not conflate service `pinned` with periodic monitoring. Disabled checks suppress original and configured socket rediscovery after capability-confirmed application; new checks default paused. Existing enabled checks stay unchanged. Keep TV mode explicit for small CSS viewports, mobile touch targets, selected-service summaries, safe device preferences and absolute remembered-session expiry/revocation. Finish a viewer-only wallboard credential before recommending owner sessions on shared/public displays.

## P0 / gate 1: install once, genuinely recover later

Review `internal/install`, `internal/servicehost`, agent setup, CLI and packages. Audit 8 now provisions/checks the Linux identity and confines private state ownership/publication. Prove it on actual systemd and finish interruption rollback; process evidence alone does not accept installation. Use deliberate Windows restricted identities/ACLs. Paths in the installer and protected data directory must agree; compiling a unit containing User=monik does not create that account.

Implement independent native recovery for updating the service-host itself. An immediately crashing new supervisor must not be the only component able to roll back. Preserve signed current/previous slots, version/digest/journal identity, compatible worker state and spool. Local IPC needs request correlation, bounded input, concurrency and deadline handling, particularly Windows file-based request/reply transport.

Evidence: real systemd and SCM, before-login boot, stop/restart, killed worker/supervisor, invalid entrypoint, full disk, denied write, interrupted activation and re-upgrade after rollback. All credentials remain protected. No shell through the agent, automatic host reboot or unrelated application control. The owner explicitly requested a separate manual SSH console in Audit 8; its fixed-target/authentication boundary must remain independent.

## P0 / gate 2: immutable signed releases and durable rollout

Finish immutable release publication so importing B cannot replace the metadata/bytes promised to a pending operation for A. Verify and stage all content before catalogue publication; serialize or atomically publish manifests. Pin exact authenticated target digests and trust state independently of operation parameters.

Use a maintained TUF client where feasible. Complete root/key rotation, expiry/replay defense and high-water recovery. Keep private signing custody outside the runtime controller. Persist canary/platform cohorts, batches, pause/resume/deadlines and failure thresholds. A rollback is a failed update with recovered availability, never a successful upgrade. Downloads must not starve heartbeat/control and progress must be real bytes/stages.

Evidence: concurrent imports and old queued jobs, corrupt/mismatched metadata, root rotation, controller restart in each cohort state, failed new process, interrupted disk writes, unauthorized receipts and actual native recovery. `operation.retry_selected` and `update.resume` stay rejected until real linked retry/rollout behavior exists.

## P0 / gate 3: full protected controller recovery and migration

Build a protected consistent archive of DB committed state, controller identity, CA/TLS, encryption/update trust keys, configuration, journals/receipts and a checksummed version manifest. A SQLite snapshot alone is not enough. Validate paths, sizes, hashes and compatibility before staging a restore into a clean destination.

After restore, reconcile newer agent revisions/generations/completed effects. Never replay old update/restart instructions or downgrade accepted trust because the restored database is stale. Enforce one authoritative writer during physical migration. Complete scheduled/armed move uncertainty, disconnected cancellation, endpoint expiry and trust retirement. Unknown new addresses cannot reach offline agents without a previously authorized route.

Evidence: busy backup/empty restore, deliberately absent key, old DB versus newer worker, interrupted restore, writer fencing, overlapping/non-overlapping endpoint changes and reassigned old endpoint. No tests against the owner's active controller without an explicit deployment plan.

## P1 / gate 4: complete monitoring policy, capabilities and service health

Extend the real v6 rule contract only through selectors and tests: per-profile/per-agent overrides, available RAM/absolute disk space, named sensor temperatures, ping/latency/TLS thresholds and historical version anchors. Defaults are workload-dependent; do not enable one universal hardware alarm. Rule edits do not reclassify historical raw evidence or count as physical recovery.

Complete maintenance editing/recurrence only with explicit timezone/DST semantics. A bounded recent list is not the full maintenance archive; add pagination/filtering when needed. Acknowledged, under maintenance, paused and healthy are separate dimensions. Keep critical escalation unread once, not repeatedly flapping. Preserve evidence of acknowledgement transitions.

Custom service requests already execute on the agent under destination/method/body budgets. Keep health expectations separate from HTTP responsiveness, explicit configured success from baseline errors, and denied/missing evidence from failure. Add multiple checks per service with a clear primary/rollup contract before readiness/liveness/TCP/gRPC adapters. Prefer explicit allowed metadata/manifests over path crawling or choosing any green response. No auto POST or raw secret-body retention.

Prove per-collector scheduling, deadlines, pause-with-control and versioned capability enforcement. Secret refresh/offline restart/invalidation, per-check scoped trust and address authorization deserve a focused next pass. Never accept unknown configuration fields as silent no-op success.

## P1 / gate 5: storage, history, coverage and operational bounds

V6 cleanup and indexed diagnostics are bounded but do not implement 30/180-day aggregates. Complete the owner retention contract (48h raw, 30d minute, 180d coarse) or obtain an explicit revised measured capacity. Preserve min/max, sample counts, durations, missingness and rule/inventory anchors. Means of percentiles are not overall percentiles. Earliest/latest records are not coverage.

Measure retention/ingestion/exports concurrently on a populated database. New indexes/migrations must be reviewed for write-lock duration. Bound operation, incident, rule/config, admission, dedup and event history consistently with allowed retries and retained explanations. Keep drop/loss intervals durable. Never acknowledge uncommitted telemetry under disk pressure.

Add historical service/rule/name navigation and time-zone/DST edge tests. Raw export is already bounded and authenticated; larger asynchronous/export formats are optional only with cancellation and quotas. Do not confuse diagnostic or sample export with full restore backup.

## P1 / gate 6: finish everyday interaction and secure admission

Run the expanded browser fixture against the exact compiled build, then native agent smoke tests. Verify rule edit/conflict, maintenance start/cancel, admission window expiry and already-pending survival, recent-auth same-key continuation/cancel, password change with two sessions, unread escalation and corruption errors. No weakened assertions or synthetic metrics relabeled as real host data.

Retain dense/optional-card overview, per-machine pin, stable ordering/search, grouped service expansion, focus/drafts, charts and all six ranges. Show actual collection age separately from browser stream state. Add bounded support diagnostics and explicit managed-install readiness where every green indication has corresponding evidence.

The new admission window is a public exposure reduction, not DoS protection. Consider explicit allowlisted networks, a reject/retry policy, bounded tombstone retention and an optional enrollment challenge. Keep already trusted agents unaffected and unknown machines quarantined. Never distribute a shared eternal fleet credential. Account password recovery is still needed via a protected local operator flow; no unauthenticated takeover endpoint. Full role expansion comes only with endpoint authorization tests.

## Release evidence required

Return a coherent main commit, rebuilt native release artifacts with actual version/commit metadata, checksums/signatures and a matrix separating build, foreground, install, boot, update, service-host recovery and restore. Native Windows, systemd power loss, complete v3 battery, 50-agent capacity and 24h soak remain NOT RUN in Audit 8. A green helper suite is not a complete release.

Telegram and mandatory third-party services stay deferred. Do not add Redis/Kafka/an LLM/plugin marketplace to solve a small fixed-scope problem. The goal remains: install once, know what is happening, control safely, inspect honest history and recover without losing the fleet.


## Audit 8 boundaries and next most valuable work

The owner-approved SSH gateway is an exception to the earlier no-console product scope, not permission to turn the monitoring agent into a command runner. Keep fixed local target mapping, independently checked host keys, recent owner auth, per-session tickets, WSS origin validation, bounded output and session closure. Add native SSH shell and actual xterm/browser tests before treating the echo fixture as end-to-end acceptance. Restricted viewer credentials are important before deploying a shared TV.

Preserve red priority for problematic host metrics or failing selected services only, stable ties and separate unselected-problem indicators. Extend rule sources without hardcoding safety temperatures. Check actual TV CSS viewport/zoom, long translated labels, 200% ordinary zoom and narrow layouts with visible axes and usable remote/keyboard focus.

Finish first-host systemd installation, before-login boot and independent recovery tests before more feature work. Audit 8 fixes premature supervisor return and demonstrates non-root compiled process restart/spool recovery, but not OS boot or power-cut durability. Windows restricted identities/ACLs and local request correlation remain open. Full protected backup/restore and immutable release publication remain P0. Do not disable guards to make remote deployment appear ready.
