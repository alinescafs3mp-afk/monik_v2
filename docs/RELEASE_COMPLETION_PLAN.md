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
