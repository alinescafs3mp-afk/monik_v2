# Monik: authoritative completion gates after V17

Baseline reviewed: `35fccbb0265777b12c2893667ef2fea33e953928`. V17 is a corrective audit, not a full-release declaration. This document replaces the stacked historical status notes; historical text is preserved at `history/RELEASE_COMPLETION_PLAN_THROUGH_V12.md`. Owner requirements remain authoritative.

## Working features to preserve

Go controller / SQLite / embedded Vue; enrolled agents with scoped trust and quarantine approval; host metrics and local service discovery; explicit bounded HTTP requests and secrets; separate monitoring/overview selection; durable human names; compact/mobile/TV presentation; labeled history and bounded raw export; real host rules and maintenance; recent authentication and revocable remembered sessions; read/unread incident and operation metadata; optional fixed-target SSH gateway; immutable signed worker releases and frozen canary/batch rollout with pause, observation, failure block and same-job continuation.

V12 added exact receipt ACKs, local process locks, joined shutdown, corruption rejection and genuine local signed worker replacement/failed-candidate recovery. V13 adds validated complete control/secret JSON, unambiguous health decoding, config-result provenance, durable/fair receipt handling, restart-persistent queue loss accounting, backfill independence from old jobs and address preservation. No generic operation retry or service-host self-update has been silently enabled.

V14 separates complete local-socket presence from HTTP identification, reconciles missing discoveries without losing watched outages/history, batches new checks into one revision, and adds external enrollment-profile selection plus explicit same-CA offline SAN extension. It does not change enrolled routes. Windows IPv6 parsing is corrected with byte fixtures, not native OS evidence.

V15 implements owner-prepared one-file Linux managed installation, exact source/bundle validation, single-use download authorization and evidence-based readiness. The initial V15 auditor had a limited Go 1.23 environment; host integration later passed full source/browser/process checks. V16 has its own full Go 1.27.0 source results. Native systemd/capability inheritance and service boot remain separate unproven gates; do not inflate source/build results into them.

## V17 acceptance priority

Finish actual drag/keyboard geometry acceptance before claiming the resizable interface is ready on a physical device. All rows and the common header share widths; service columns adapt and mobile fallback preserves wide-screen preferences. Mode-local storage, cancelled gestures, fresh telemetry with held order, latest graph-point responses and truthful export status are implemented. Source-only CSS guards and fake layout APIs are not visual evidence. Preserve V16's silent refresh/scroll fixes. No production Go/schema/protocol changes are introduced by V17.

## Previous V16 first-host acceptance priority

The owner has started a real first machine. Do not interpret helper/build PASS as a clean unattended install/boot test. Finish the exact browser/ping pilot corrections before expanding the fleet: stable editor scroll/focus through four background polls, nonblinking manual refresh affordance, green default 2xx/3xx versus configured failures, finite server-anchored freshness, 3-row service columns at real mobile/TV sizes, and actual service-account ICMP replies. V16 source tests and process fixtures are separate from blocked browser/kernel checks. Existing V15 units need explicit capability configuration as well as a worker update; do not re-enroll them. See V16 guide. Historical V15 toolchain limitations do not describe V16: the current audit used Go 1.27.0.

## Gates before broad release, in order

### G1. First-host native installation and recovery

Paths: `internal/install`, `internal/servicehost`, `internal/installerbundle`, `cmd/monik-installer`, controller installer endpoints, CLI, agent setup. First run the full V15 suite and build/deploy matching credential-free templates. Give the owner the prepared downloadable executable, not raw build/install instructions. Confirm one-file launch, committed managed readiness, close-terminal survival and native boot on a disposable pilot. For the first remote pilot use the selected external profile origin, verify SAN/trust from the remote host, keep independent access, and confirm `listener_inventory_v1`. Do not rewrite working LAN agents or generate a replacement CA. Confirm Linux account/permissions, native systemd install/start/stop/restart/boot without interactive login. Complete Windows restricted service identity, ACLs, correlated bounded local IPC, native SCM/boot. Make partial install/reinstall recovery explicit. Preserve identity, URLs, journals and both queue formats. Existing process fixtures do not count as service-boot evidence.

Independently recoverable replacement of the service-host remains mandatory. A supervisor that crashes before its own recovery code cannot be its only recovery mechanism. Keep current explicit guard until actual OS-backed recovery passes bad-entrypoint, interrupted activation, write denial/full disk, network loss and re-upgrade tests.

### G2. Protected complete controller backup/restore

Back up committed DB state plus controller identity, CA/TLS, encryption keys, settings, trust/high-water state, immutable releases referenced by jobs, and a checksummed version manifest. Verify into a clean destination before activation. Reconcile newer agent revisions, endpoint generations, completed receipts and held/claimed update jobs; do not replay an old effect or downgrade accepted trust. One local directory lock does not fence two separately cloned writable VM controllers.

Evidence: busy/empty restore, old snapshot versus newer agents, deliberate missing keys, interruption and one-writer physical migration. Existing DB-only helpers or fail-closed startup checks are not a completed restore feature.

### G3. Actual fleet release lifecycle and bounded resource use

Keep the implemented immutable catalogue and per-platform canary/wave state machine. Complete immutable-release quotas/GC without deleting pending/current/rollback references; key/root rotation and expiry recovery; fair bounded download versus heartbeat/control; real transfer progress. Native signed Linux and Windows rollout across multiple test machines, outage, pause/resume, timeout and uncertain effects must be exercised. Do not downgrade a controller during an active V11+ plan.

Application-health gates and optional canary choice need explicit criteria, not arbitrary green HTTP. Generic selected retry stays unavailable until linked operations, fresh preconditions and immutable eligibility are implemented and tested.

### G4. Long-term observability and operational limits

Implement the retained requirement: 48h raw, 30d minute and 180d coarse history, or explicitly obtain a revised measured capacity from the owner. Preserve extrema, counts, quality/coverage, configuration/rule/inventory anchors. Measure concurrent retention, exports, backup and ingestion, disk/WAL growth and delayed collectors. First/last samples or a broad queue-loss interval are not complete coverage.

Bound metadata/dedup retention consistently with replay windows. V14 hides inactive discoveries but intentionally retains their request definitions and history; implement future bounded retirement/GC with owner selection and recovery anchors, not deletion just because a port closed. V13 makes queue loss counters durable but does not reconstruct pre-upgrade loss or guarantee Windows power-cut durability. Expand rules only with workload-specific selectors, absolute free space, known sensors and historical semantics. Multiple check types need a primary/aggregate contract before adding protocols.

### G5. Everyday access and safety

Complete a limited viewer/wallboard credential before recommending owner sessions on shared TVs. Retain recent-auth, CSRF, exact operation results and unknown-result feedback. Complete protected local forgotten-password recovery; no unauthenticated takeover endpoint. Secret refresh/offline cache and per-check trust need native recovery tests. Keep SSH separate from agent authority, with fixed targets and pinned host keys.

Browser acceptance must run the exact bundle, including pending old-check results, request editor, read/CAS conflicts, lost responses, display selection, long labels, keyboard, 390px mobile, 960px CSS TV, 200% ordinary zoom and restricted storage. Test physical TV/Yandex separately; synthetic browser/SSH fixtures are not actual endpoint evidence.

## Acceptance decision

Return a coherent main commit after local integration, exact source/build hashes, platform matrix, test logs and unresolved gates. Distinguish build, helper, integration, real process, native service, boot, recovery and restore evidence. No 24-hour soak, 50-agent capacity or complete v3 claim until measured. No Redis/Kafka/LLM framework, mandatory cloud service, Telegram dependency, remote shell through the agent or TLS bypass is authorized by this completion plan.
