# Monik release completion plan after audit 4

Date: 2026-09-14. This section supersedes earlier statements that custom request editing, per-check intervals, typed response expectations or local health suggestions are wholly absent. Those bounded features are implemented in Audit 4. The following roadmap items are **not** newly implemented by mentioning them.

## First, preserve this pass

Use the exact source/patch from `4f85de3`. Keep request_version/capability validation, strict server + worker checks, paused-state preservation, nonblocking trial, bounded scheduler, negative controls, and rule/transport distinction. Run the actual browser and native journeys in `GROK_AUDIT4_HANDOFF.md`. A recent green liveness endpoint is not permission to erase a failing readiness endpoint or existing owner check.

## Priority 0: still mandatory, not displaced by more probing features

Independent native recovery for service-host replacement remains unimplemented. Signed worker activation/probation corrections are not that recovery mechanism. Complete restricted OS identities, Windows ACLs and before-login boot; correlate and bound local supervisor commands/replies. Test killed worker/supervisor and entrypoint failure on real Linux and Windows.

Publish immutable versioned release files, preserve queued-job exact metadata/bytes, stage imports atomically and maintain persisted canary/batch/pause/resume control. Prove crash recovery and rollback-safe state formats. Coordinate old/new custom-check profiles on downgrade.

Complete a protected whole-controller backup/restore, key custody, version reconciliation with newer agents and one authoritative writer. Do not lose the encryption key, reuse an old job cursor as proof, regenerate trust on corruption or claim that a SQLite snapshot alone restores the platform. Measure FULL synchronous I/O on actual storage. Long-term aggregates, actual custom rule/maintenance evaluation and fleet-scale limits remain release work, not accepted features.

## New high-value follow-through: easier setup without false health

| Priority | Idea / code surface | Concrete implementation contract / acceptance |
|---|---|---|
| P1 | Separate readiness, liveness and optional functional check on one service | Extend identity from one primary definition to check IDs with explicit purpose/vantage. Separate timelines and roll-up policy. Never pick any passing check as the whole service healthy. Preserve existing primary IDs/history in migration. |
| P1 | Manual TCP connect and narrow protocol adapters for unresolved listeners | TCP proves reachability only. gRPC standard health, Redis PING or other adapters require explicit typed protocol/authorization and native fixtures. Do not send arbitrary HTTP to known non-web software or introduce raw shell probes. |
| P1 | Declarative local service health contract | Optional owner-authored manifest with exact listener/path/method/purpose/expected result/secret references. Prefer this over heuristic routes. Only the already discovered local destination; schema/version/size bounds; no execution fields; explicit approval for POST. An HTTP-provided manifest is untrusted evidence, not authority to expand destinations. |
| P1 | Small versioned service-template registry | Templates for documented readiness APIs, with source URL, supported versions, cost class and expected semantics. Process/metadata hints select suggestions, not silent credentials or expensive calls. No LLM or cloud runtime dependency. |
| P1 | Operator-controlled advisor policy | Per-agent/off/allowlisted-path budget, excluded services, backoff after 429/503 with bounded Retry-After, and explanation of skipped candidates. This pass has finite safe defaults and cache, not all these controls. |
| P1 | Request diff and trusted recent-auth workflow | Display exact normalized method/URL/path/header names/body secret references and expected conditions before fleet changes. One shared reauthentication prompt for secrets/updates; preserve draft without storing passwords. |
| P1 | Durable secret availability/invalidation | Persist only appropriately protected agent-scoped secrets where authorized, survive offline restart, rotate without stale cache use, preserve stable references, prevent secret values in export/evidence. Current cache is memory-only. |
| P1 | Per-check CA and mTLS | Trusted scoped CA/client-secret references, cert expiry, same-origin isolation, native tests. Do not confuse controller CA with application-service trust; no global insecure fallback. |
| P1 | Scheduler visibility | Record due/start/end/late/skipped/budget counts and active probes; expose per-service data age and config version. Meter per-agent rate/cost and distribute phase so many 30s checks do not spike together. Validate responsiveness under body timeouts, disk pressure and downloads. |
| P1 | Diagnostic evidence on demand | Return structured bounded error layer/errno, HTTP status and whitelisted predicates. A privileged redacted short body excerpt would need explicit retention/secrecy rules; raw responses are deliberately absent today. |
| P2 | Small protocol-aware cURL importer | Parse offline into the typed request editor and preview; never execute shell or substitutions, reject files/proxy/redirect/insecure/unbounded options, turn credentials into secret references. Useful convenience, not required for current manual editor. |

Do not expand the runtime into an infrastructure automation center. No subnet crawling, unauthenticated admin bypass, browser-based inference tests, arbitrary POST health guessing or script-defined plugin execution. A predictable negative result is preferable to a fabricated green one.

## Original broader release gates retained below

The following prior plan is retained for completeness. Its older references to features being absent must be read with the implemented Audit 4 subset above. Historical numerical test counts are not current evidence. Work until each required gate has real implementation plus native/test evidence, not merely additional documentation.

---

# Monik: finish the product without expanding it into an orchestration platform

Authority: the owner's approved v3 requirements, later UI requests, and the cumulative audit 3 source corrections. This is a completion directive for the local implementer, NOT evidence these tasks are already implemented. Do not remove explicit unsupported-action guards until the corresponding actual behavior and tests exist.

## Objective

Install once, select important machines in the web UI, see trustworthy host/service states, investigate any retained period, centrally configure/update/rebind agents, and recover from failures without losing identity or inventing success. The running system must not depend on cloud LLMs. No Telegram/email release dependency, arbitrary remote command execution, host reboot, unrelated service/container control, scanner, plugin marketplace, mandatory external database or HA cluster.

All four delivery dimensions matter: working implementation, usable UI, native operational evidence, and truthful documentation. Compilation does not replace any of the other three.

## Order and stop rules

Implement the following gates in order, preserving functioning monitoring. Finish a vertical scenario before starting another framework. Keep main coherent, run regressions and commit each reviewable slice; never force-reset concurrent owner work. Do not mark the product released while a P0 below is open. When a target OS, permission or device is unavailable, record the exact missing test rather than replacing it with a mock and a green status.

### Gate 0: integration and operability baseline

Apply exactly one cumulative/incremental patch route, verify hashes, build the UI and server from matching source, and run all tests. The overview must show only pinned machines in compact rows by default. Preserve card fallback, collapsible sidebar, chart axis labels, feedback states and all six ranges. Run the browser scenario against its local synthetic fixture, then run a real native agent against a disposable controller. Verify actual schema/worker version and desired/applied revision/hash.

Deliver an explicit supported-platform table: build supported, foreground tested, installer tested, pre-login boot tested, worker update tested, service-host recovery tested. Never merge these into one checkbox.

### Gate 1: native install-and-forget (P0)

**Paths:** `internal/install`, `internal/servicehost`, `internal/agent/setup`, packaging, CLI, add-machine UI.

- Choose a concrete privilege boundary for worker, supervisor and installer. Create/validate Linux service identity and state ownership. Choose Windows service identity and ACLs deliberately. A monik user entry in a unit is not user creation; root-created 0700 state is not readable by an unrelated account.
- Make enrollment/install restartable with process locking and durable phases. Handle concurrent installer invocation, uncertain HTTP completion, expiry, interrupted credential/config writes, existing enrollment, repeated install, paths with spaces and Unicode, and refused permissions. Never overwrite a working identity through normal setup.
- Correlate every local supervisor request/reply by request ID and expected operation/version; use a bounded message size, queue/concurrency and deadline. Fixed request/response files must not be treated as a safe multi-request protocol. No remote shell or arbitrary file command.
- Implement independently recoverable replacement of the service-host itself through a native installer/watchdog boundary that survives an immediately crashing new service-host. The installed code being replaced cannot be the only recovery mechanism.
- Define disk reserve, current/previous slot, signed desired digest, state schema compatibility and rollback authorization. A worker process that merely exists is not a verified upgrade. Network loss is not a reason to oscillate versions.
- Preserve both legacy and v2 spool directories across supported rollback. Explain whether an older worker temporarily cannot drain newer records. Reconcile rather than delete uncertain records.

**Evidence:** real Linux systemd and real Windows SCM, boot without interactive login, stop/start, killed worker, killed supervisor, failed entrypoint, disk full, denied write, interruption at each activation boundary, lost server contact, restored contact, upgrade then rollback then re-upgrade. Capture versions/digests/jobs and protection of credentials. Gate remains open until both worker and service-host pass.

### Gate 2: controlled signed publishing and rollout (P0)

**Paths:** `internal/tufutil`, `internal/update`, `internal/server/lifecycle.go`, release tool, Updates UI.

- Publish immutable release directories or content-addressed objects; a queued operation names authenticated metadata and exact target bytes for one release. Do not reuse one mutable target path for concurrent releases.
- Stage and fully verify all metadata/targets before atomic catalogue publication. Serialize import or use a transactional manifest pointer. An invalid high-version candidate must not advance trusted state. Detect duplicate IDs with different bytes.
- Adopt a maintained TUF implementation where feasible rather than keep increasing custom crypto/protocol code. Independently enrolled root, timestamp/snapshot/targets linkage, length/hash/version/expiry/rollback defenses, key rotation and trusted-root persistence are mandatory regardless of library.
- Persist cohort/canary/batch/pause/resume/deadline state. Preflight each OS/arch and eligible version. Freeze targets, make offline disposition visible, and pause next activation when health/rollback thresholds fail. Resume must continue the actual rollout, not issue a fresh untracked batch.
- Use bounded downloads independent from heartbeat/control. Show real byte progress, verification/activation/probation/confirmation, offline waiting and error evidence. Cancel only work that can actually be cancelled; no fabricated percentages.
- Recovered previous version means failed update with rollback, not update success. Avoid recurring activation of a failed release. Keep explicit security version floor and digest-authorized local recovery separate from replay of old metadata.

**Evidence:** two imports while old jobs wait, tampered/mismatched metadata, root rotation, missing platform, truncated transfer, repeated request, controller restart in every cohort state, rejected receipts, disk full, healthy canary then failing cohort, actual OS recovery. New artifact must not modify queued older jobs.

### Gate 3: protected full-state backup, restore and rebind (P0)

**Paths:** server CLI, TLS/secret/config storage, jobs, rebind, operator runbook.

- Build a consistent protected controller archive: database including committed WAL through a proper backup interface, controller identity, TLS/CA material, secret-encryption key, configuration, enrolled update trust/high-water state, versions and checksummed manifest. Never put any of these live secrets in Git or normal diagnostics.
- Restore only into an explicitly chosen clean destination, validate archive paths/size/checksums/version before activation, stage/verify atomically, and prove an old backup cannot silently regain authority over newer agent state.
- Restore mode needs actual reconciliation: compare agent config revisions, endpoint generations, completed receipts and update trust state. Do not replay an old restart/update or downgrade accepted trust. Unknown effects remain unknown until evidence resolves them.
- Establish one active writer during physical migration. A generation field does not fence two independent writable databases. Address rollback is not starting an old stale database after the new writer accepted data.
- Finish migration plan delivery/receipt, unverified future address, scheduled arming with time uncertainty, primary-loss trigger, candidate authenticated trial, committed confirmations, offline cancellation and old endpoint/trust retirement. Expiring an unactivated plan does not undo an already confirmed primary.
- Add protected local recovery flows for truly disconnected agents; they preserve identity/history where possible. Never claim unknown-IP recovery without a preconfigured route/DNS/fallback.

**Evidence:** backup on a busy disposable controller; empty restore; deliberate missing CA/key; old snapshot with newer agent revisions; power cut during staging; two-writer prevention; overlap and non-overlap endpoint moves; offline unprepared agent truth; old endpoint reassigned to another authority. Never test these on the owner's active controller without a separate deployment plan.

### Gate 4: monitoring semantics and complete controls (P1, before general release)

**Paths:** protocol validation, runtime/checks, collectors, rule evaluator, config history, Problems/settings UI.

- Maintain separate machine contact, collector quality, HTTP responsiveness, configured application health, service discovery presence and checker vantage. Missing data never resolves a confirmed problem and must not preserve old green state.
- Connect user rules to the actual evaluator, with effective version, selector, thresholds, persistence, coverage, hysteresis and recovery evidence. A raised threshold ends a rule/policy state explicitly; it does not rewrite yesterday's observations.
- Implement maintenance in a durable interval model that keeps observations, explains suppression and recovers automatically at the chosen end. Acknowledgement is not recovery; hidden/pinned is not monitoring enablement.
- Complete config field/unknown-field validation. A accepted profile must either change supported behavior with an applied hash/revision or clearly reject the field. Do not save unused settings as a successful operation.
- Implement selected-target retry as a new linked operation with fresh preconditions and immutable eligible scope. Do not retry already successful disruptive effects or resurrect expired commands. Expose revision conflicts and per-target reasons.
- Make collector capabilities explicit by platform: known sensor identity, CPU counter delta baseline, RAM available, local filesystem exclusions/deadlines, ICMP sent versus permission failure, network counter reset/sleep/resume. Optional unsupported sensor data is not zero.
- Bound slow host APIs, probes, discovery, Docker metadata and release downloads independently. Update/pause must not disable the control channel. Preserve collector-specific observation timestamps and missed-run counters.
- Present safe HTTP response evidence by configured method/path/Host/SNI: status, duration, content type, bounded known health words. Auth requirements/HEAD/SPA fallback/HTML/contradictory JSON must remain clear. Explicit health expectations need exact bounded validation; do not guess a database is healthy from 200.
- Inventory discovery must say what was not inspected (virtual hosts/namespaces/containers/permissions/budget). Add suggested health endpoints from known documentation/approved metadata only as previewed suggestions, not unbounded path crawling. A manually approved URL uses the same local probe policy and secret handling.

**Evidence:** brief/persistent CPU spikes, changed sampling period, missing collector, sleep/resume, HEAD fallback, conflicting health fields, 401 versus connection failure, body budget, remote/local vantage difference, stale discovery, container recreation, cancelled/late jobs and gap-preserving incidents.

### Gate 5: history, storage and forensic usefulness (P1)

**Paths:** SQLite schema/queries/retention, history/chart/Problems/export UI.

- Implement and measure 48h raw, 30d minute and 180d coarser retention from the v3 contract, or explicitly negotiate a different measured capacity. Do not quietly drop requested history to pass benchmarks.
- Preserve extrema, counts, durations, coverage and retained configuration/rule/inventory anchors. Aggregating 95th percentiles by averaging is invalid. A minute bucket must not masquerade as a sample measured at a chosen second.
- Add bounded retention batches and suitable time/entity indexes. Include WAL/checkpoint/backlog metrics. Long read snapshots, deletes, export and backup cannot starve live ingestion/control. Full disk must not acknowledge uncommitted samples.
- Bound event, session, enrollment, operation, receipt, dedup and export metadata by compatible retention horizons. Job/observation dedup cannot expire before a replay can arrive. Durable loss intervals survive restart and report data dropped by spool/retention without fabricated coverage.
- Keep historical incident search stable for equal timestamps; link service incidents to their machine/check and display human names without losing immutable IDs. Current status filters must not pretend they describe the exact status known at time T.
- Finish range exports beyond the new bounded raw JSON subset as actual requirements warrant: service UI entry, provenance/coverage, safe CSV optional, async export with cancellable bounded work only when needed. A sample export is never the full protected controller backup.
- Test timezone offset, midnight, DST ambiguity and nonexistent local time explicitly; storage uses UTC. Fixed-history links retain absolute bounds and selected entity after navigation/reload.

**Evidence:** generate a measured fleet dataset, query recent raw and older aggregates, confirm a single five-second failure/CPU peak remains visible, force collection gaps, backfill/duplicate data, delete expired raw while preserving explanations, run retention concurrent with ingestion/export and inspect WAL growth. Record machine specs, number of agents/checks, disk bytes and latency percentiles. No unmeasured capacity claim.

### Gate 6: useful web interface, not just working endpoints (P1)

**Paths:** Vue app/shell, actions, history, setup, updates, machine/service details, browser tests.

- Every action has immediate local feedback, a durable server result and per-target delivery/application/evidence when applicable. No spinner without a reason, no accepted-as-applied, no all-green partial fleet result, no invented progress.
- Session/network/SSE errors are distinct. Keep forms/focus/selection while metrics refresh; preserve last known values with an explicit stale label. Slow/failed requests must not switch the user to login unless authentication actually failed.
- Add a reusable recent-auth prompt for sensitive operations; no plaintext password in browser storage. Add local-owner password change and recovery procedures, current-session revocation and idle/absolute lifetime clarity. Evaluate a simple viewer role only after every endpoint is authorized consistently; do not grow an enterprise RBAC framework prematurely.
- Per-row pin, rename, check configuration and bulk operations have independent pending/error states. "All matching" differs from "this page"; preview/freeze disruptive targets. Never overwrite per-agent profile exceptions without preview.
- Overview has search, stable sorting, groups/problems filters, selected/hidden/offline counts and a bounded service summary. Add bulk pin/unpin and display-density controls only when small and tested; do not infer monitoring selection from screen visibility.
- Charts retain readable axis text and units, fixed proportional percentage scales, responsive time ticks and keyboard cursor. Link events, updates and config changes without asserting unproven causality. Do not mix unrelated metric units on one unexplained axis.
- Validate long names, hundreds of services, zero machines/no pins, offline-only fleet, 200% browser zoom, 390px width, keyboard/focus, reduced motion, blocked browser storage and Russian/English technical identifier boundaries. Browser fixture uses synthetic telemetry; a separate real-agent smoke test proves collection.
- Add a self-diagnostics/compatibility panel with actual version/build, protocol range, last live contact versus sample age, config lag, spool/collector errors, certificate expiry, backup age and unfinished capabilities. Keep one-click bounded redacted support export, not unrestricted logs/files.

**Evidence:** full Chromium/Firefox where available against exact build, no console errors, real screenshots, asserts for download/unknown-result/conflict/offline/cancel/refresh. Keep browser CI failures visible and never weaken assertions to make an audit green.

## Additional checks worth retaining after launch

- Signed release provenance, dependency lockfiles, SBOM/license notices and dependency vulnerability checks against trusted upstream sources. This package does not claim a vulnerability database scan in the isolated environment.
- Agent cloning/duplicate identity detection, planned retirement and orphan inventory lifecycle without erasing past incidents.
- Time source uncertainty and certificate lifetime policy, key custody and root rotation with long-offline agents.
- Bounded diagnostic exports and parameter/body redaction at ingestion as well as presentation. No password, bearer credential, full response body or private key in screenshots/logs/artifacts.
- Operator-run independent watchdog can later detect the controller's complete outage. A dead controller cannot report through its own UI. Telegram and external notifications stay deferred.
- Read-only Docker metadata and IPv6/vhost/platform support remain explicit capabilities. Do not silently grant Docker daemon/root authority or install hardware drivers for a missing temperature tile.

## Release decision and delivery back to the owner

Return a coherent main commit, source/release checksums, a platform matrix, actual test logs and a requirement-to-evidence ledger. Enumerate implemented, test-only, deferred and unsupported items separately. A green test suite validates the tested subset only. Mandatory worker AND service-host update/recovery, controller recovery, safe rebind, observable UI actions and truthful history may not be silently deferred to a hypothetical next release.
