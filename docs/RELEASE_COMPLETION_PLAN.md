# Monik: authoritative completion gates after V13

Baseline reviewed: `5fef10f15b610ff79b036f040d45037175b28866`. V13 is a corrective audit, not a full-release declaration. This document replaces the stacked historical status notes; historical text is preserved at `history/RELEASE_COMPLETION_PLAN_THROUGH_V12.md`. Owner requirements remain authoritative.

## Working features to preserve

Go controller / SQLite / embedded Vue; enrolled agents with scoped trust and quarantine approval; host metrics and local service discovery; explicit bounded HTTP requests and secrets; separate monitoring/overview selection; durable human names; compact/mobile/TV presentation; labeled history and bounded raw export; real host rules and maintenance; recent authentication and revocable remembered sessions; read/unread incident and operation metadata; optional fixed-target SSH gateway; immutable signed worker releases and frozen canary/batch rollout with pause, observation, failure block and same-job continuation.

V12 added exact receipt ACKs, local process locks, joined shutdown, corruption rejection and genuine local signed worker replacement/failed-candidate recovery. V13 adds validated complete control/secret JSON, unambiguous health decoding, config-result provenance, durable/fair receipt handling, restart-persistent queue loss accounting, backfill independence from old jobs and address preservation. No generic operation retry or service-host self-update has been silently enabled.

## Gates before broad release, in order

### G1. First-host native installation and recovery

Paths: `internal/install`, `internal/servicehost`, CLI, agent setup. Confirm Linux account/permissions, native systemd install/start/stop/restart/boot without interactive login. Complete Windows restricted service identity, ACLs, correlated bounded local IPC, native SCM/boot. Make partial install/reinstall recovery explicit. Preserve identity, URLs, journals and both queue formats. Existing process fixtures do not count as service-boot evidence.

Independently recoverable replacement of the service-host remains mandatory. A supervisor that crashes before its own recovery code cannot be its only recovery mechanism. Keep current explicit guard until actual OS-backed recovery passes bad-entrypoint, interrupted activation, write denial/full disk, network loss and re-upgrade tests.

### G2. Protected complete controller backup/restore

Back up committed DB state plus controller identity, CA/TLS, encryption keys, settings, trust/high-water state, immutable releases referenced by jobs, and a checksummed version manifest. Verify into a clean destination before activation. Reconcile newer agent revisions, endpoint generations, completed receipts and held/claimed update jobs; do not replay an old effect or downgrade accepted trust. One local directory lock does not fence two separately cloned writable VM controllers.

Evidence: busy/empty restore, old snapshot versus newer agents, deliberate missing keys, interruption and one-writer physical migration. Existing DB-only helpers or fail-closed startup checks are not a completed restore feature.

### G3. Actual fleet release lifecycle and bounded resource use

Keep the implemented immutable catalogue and per-platform canary/wave state machine. Complete immutable-release quotas/GC without deleting pending/current/rollback references; key/root rotation and expiry recovery; fair bounded download versus heartbeat/control; real transfer progress. Native signed Linux and Windows rollout across multiple test machines, outage, pause/resume, timeout and uncertain effects must be exercised. Do not downgrade a controller during an active V11+ plan.

Application-health gates and optional canary choice need explicit criteria, not arbitrary green HTTP. Generic selected retry stays unavailable until linked operations, fresh preconditions and immutable eligibility are implemented and tested.

### G4. Long-term observability and operational limits

Implement the retained requirement: 48h raw, 30d minute and 180d coarse history, or explicitly obtain a revised measured capacity from the owner. Preserve extrema, counts, quality/coverage, configuration/rule/inventory anchors. Measure concurrent retention, exports, backup and ingestion, disk/WAL growth and delayed collectors. First/last samples or a broad queue-loss interval are not complete coverage.

Bound metadata/dedup retention consistently with replay windows. V13 makes queue loss counters durable but does not reconstruct pre-upgrade loss or guarantee Windows power-cut durability. Expand rules only with workload-specific selectors, absolute free space, known sensors and historical semantics. Multiple check types need a primary/aggregate contract before adding protocols.

### G5. Everyday access and safety

Complete a limited viewer/wallboard credential before recommending owner sessions on shared TVs. Retain recent-auth, CSRF, exact operation results and unknown-result feedback. Complete protected local forgotten-password recovery; no unauthenticated takeover endpoint. Secret refresh/offline cache and per-check trust need native recovery tests. Keep SSH separate from agent authority, with fixed targets and pinned host keys.

Browser acceptance must run the exact bundle, including pending old-check results, request editor, read/CAS conflicts, lost responses, display selection, long labels, keyboard, 390px mobile, 960px CSS TV, 200% ordinary zoom and restricted storage. Test physical TV/Yandex separately; synthetic browser/SSH fixtures are not actual endpoint evidence.

## Acceptance decision

Return a coherent main commit after local integration, exact source/build hashes, platform matrix, test logs and unresolved gates. Distinguish build, helper, integration, real process, native service, boot, recovery and restore evidence. No 24-hour soak, 50-agent capacity or complete v3 claim until measured. No Redis/Kafka/LLM framework, mandatory cloud service, Telegram dependency, remote shell through the agent or TLS bypass is authorized by this completion plan.
