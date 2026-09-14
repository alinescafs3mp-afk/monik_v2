# Monik Audit 6: operational controls and truthful incident handling

Date: 2026-09-14. Reviewed baseline: `e771f35343b0c3f9aaba7db446d2ffd1afc3aa97`, tree `a4e92aa2286f3540982463768bd45752fe025d5c`.

**PRE-RELEASE.** This report describes the accompanying source snapshot, not the owner's running installation. The production controller and agents were not contacted or modified. The current source retains the previous five audit integrations. The delta patch applies once to the baseline above; older audit patches are not prerequisites.

## 1. Scope and review method

Reviewed the incoming source, relevant prior integration status, enrollment, incident evaluation/history, configuration application, authentication/session lifecycle, operation acknowledgement, storage cleanup/diagnostics and the corresponding Vue interfaces. Executed the baseline race suite, added failing regressions, implemented targeted corrections, reran tests/builds and checked patch reproducibility. This is not a claim that every line, dependency vulnerability, native platform behavior or possible failure has been exhaustively verified.

The main priority is to finish useful bounded scenarios rather than add another framework. Telegram, external notifications, shell execution, host reboot, monitored-application control and production deployment remain out of scope.

## 2. Delivered behavior

### 2.1 Real global host rules

`rule.save` is no longer a phantom action. It now accepts exactly one CPU, used-RAM and most-filled-local-disk percentage rule. Each has warning, critical and recovery thresholds plus persistence and recovery durations. The current defaults remain 85/95 percent with recovery below 80, 60 seconds persistence and 30 seconds recovery. They are adjustable starting settings, not hardware limits or universal service objectives.

The Settings form exposes the actual contract. Validation requires finite percentages, `0 <= recovery < warning < critical <= 100`, warning at least 1, and integer durations from 5 to 3600 seconds. Unknown metrics and extra fields are rejected. Per-host overrides, temperature rules, absolute free-byte disk limits and arbitrary expression evaluation are NOT silently accepted.

A save compares the base revision and atomically stores a version with effective time. The evaluator uses the effective rule version and observation time. Old pre-policy samples cannot reopen today's incident under the new rule. Raising a threshold ends the prior incident as `policy_changed`, not measured recovery; the previous rule anchor stays available. Pending/recovery continuity, gaps and hysteresis are tested. Today's overview uses today's thresholds without rewriting historical raw measurements. Full retroactive/bitemporal incident recomputation is not implemented.

### 2.2 Maintenance with preserved measurements

`maintenance.set` and the new `maintenance.cancel` have actual durable behavior. Scope is the fleet, one agent including its services, or one service. The web UI creates fleet windows in Settings and machine windows in Machine -> Agent; a service-only API scope exists, but a dedicated service-only creation UI is not claimed.

Windows can start now or up to 30 days ahead, last at most seven days and require a bounded reason. The server clamps a just-past immediate start to commit time instead of retroactively suppressing observations. Start is inclusive; end is exclusive. Cancellation records its own immutable time/actor and never deletes the original interval. Repeated cancellation is idempotent. Active/scheduled windows are prioritized ahead of ended records in the bounded list; truncation remains visible.

Monitoring and real incident creation continue. The overview retains its unread-open total and separately reports the count outside current maintenance when different. Host/service badges and incident evidence distinguish planned work from actual health. Historical evidence distinguishes maintenance observed during an incident from maintenance active now. No external-notification subsystem is implemented here. Repeating schedules and editing existing windows are still deferred; cancel and create a new interval instead.

### 2.3 Controlled automatic admission

A new owner-controlled admission window is exposed on Add machine. UI presets are 15 minutes, one hour, four hours and 24 hours; the validated API accepts 0..1440 minutes. Zero closes it. It uses server time, recent authentication and compare-and-swap revision checking.

**Default after upgrade: closed to previously UNKNOWN agent identities.** Existing authenticated agents, one-use-code enrollment, already-pending candidates and approved candidates are not stranded by closing or expiry. The proof lookup happens before the unknown-identity gate. Unknown agents receive `admission_closed` and retry with the same persisted identity, not a new enrollment each time. The compatible worker recognizes this state explicitly. The owner still checks the registration fingerprint and approves a candidate; the window never enables unauthenticated fleet access.

This changes public admission deliberately. Tell the owner to open the window before installing a new automatic-profile agent. It is not a network failure when a closed window prevents appearance of an unknown candidate. The gate does not claim to eliminate traffic exhaustion or replace upstream access controls. Native service installation and ACL gaps are unchanged.

### 2.4 Unread incidents and useful navigation

Problems gains an acknowledgement filter and the inverse action `incident.unacknowledge`. The overview incident link opens the unread filter. Current inventory names and parent-machine links replace bare IDs where available; they are explicitly current names, not fabricated historical names.

A read WARNING that escalates to CRITICAL becomes unread once. Peak incident severity is retained until recovery, avoiding repetitive read/unread oscillation when values cross the warning/critical boundary. A new critical escalation is audited. Explicit unacknowledgement records the previous acknowledgement in audit history. Read/unread never changes underlying health or resolves an incident. `policy_changed` is searchable separately from actual recovery.

### 2.5 Recent-authentication dialog and owner password

Sensitive operations now invoke a reusable native HTML dialog when the server specifically requests recent authentication. Focus/background behavior uses the browser dialog, Escape/cancel is supported, and password input is cleared rather than stored. After successful reauthentication, the same action retries ONCE using its original idempotency key. Cancel creates no action. A lost mutation response still uses the existing unknown-result reconciliation; it is not permission for unlimited resubmission.

Settings includes an owner password change using the current password. The new password must differ and be 12..1024 UTF-8 bytes. A bounded endpoint, CSRF protection and attempt rate limit apply. Updating the password hash and deleting all browser sessions is one transaction; agent credentials do not change. The UI clears secret inputs and directs to sign-in. An uncertain response warns that the password may already have changed rather than automatically retrying the mutation.

A race was additionally fixed: an old login request verified just before a password change cannot create a new session afterward. Session creation rechecks the exact verified hash inside its insert transaction. Operations waiting for the control lock revalidate their browser session before executing. Reauthentication of a revoked/expired session cannot claim success. Local forgotten-owner-password recovery and comprehensive RBAC are not implemented by this feature.

### 2.6 Storage and diagnostics

Latest service observations and service-series reads reject corrupt/null stored JSON instead of returning an empty healthy-looking structure. Errors propagate to the relevant APIs. Equal completion times are ordered deterministically by observation ID.

Retention deletes at most 1000 rows per transaction, up to ten batches per table and within a five-second cleanup context. Raw host/service records, compatible ingest receipts, old SSE events and expired browser sessions are covered. Failure is logged. A large backlog can require multiple scheduled passes; it is visible rather than advertised as already cleared. Dedup history lasts twice the configured raw retention. Long-term aggregates, incident/config version retention and fleet capacity remain separate incomplete work.

The diagnostics endpoint reports actual journal/synchronous settings, DB/WAL bytes, indexed earliest/latest raw observations, retention lag, desired/applied revision lag and event bounds. Timestamp bounds explicitly do not prove continuous coverage. SSE detects a resume cursor older than retained event history and requests a new snapshot. New time indexes improve bounded reads/deletes but their initial creation can hold a write lock on an existing large database; test upgrade on a copy first.

### 2.7 Status consistency

A requested global pause is not displayed as an applied pause before the agent acknowledges its matching revision/hash. Once acknowledged, service summaries show paused rather than generic stale/unknown while control remains available.

An explicitly configured successful application expectation is not overridden by a blanket HTTP 5xx rule. A user can deliberately expect a particular code; the actual status is still displayed. Unconfigured baseline 5xx remains an error and a failed expectation on HTTP 200 remains a failed application check. This does not relax auto-detection into searching for any green result.

## 3. Confirmed defects and proof

| Defect/boundary | Evidence and correction |
|---|---|
| Read warning stays read after critical escalation | New regression failed on the reviewed baseline, passed after peak-severity/unread transition fix; repeated oscillation is covered |
| Corrupt latest/series service JSON silently accepted | New regression failed on baseline; JSON object checks and error propagation now pass, including literal null |
| Old-password login racing password change | Transactional password-hash precondition and session revocation regression pass; no post-change session from a previously verified stale user |
| Unsupported rule/maintenance controls | Real small contracts replace their previous unavailability guards; invalid/extra/oversized policy fields reject before operation persistence |
| Policy effect committed but result journal fails | SQLite fault injection verifies committed policy plus `unknown_result`, not false no-effect failure or success; read current policy before another action |
| Global pause ignored in service presentation | Before/after applied revision/hash tests prove pending versus paused semantics |
| Explicit expected 5xx overrides lost | Tests cover configured pass+503, baseline 503 and configured fail+200 independently |
| Unbounded cleanup and unreadable diagnostics | Temporary SQLite test creates 10,500 expired host rows; one pass leaves the bounded remainder, a subsequent pass clears it; actual FULL setting and lag are checked |
| Active maintenance crowded out by history limit | Regression creates 201 ended windows and an older active one; bounded list keeps the actionable window first |

The two baseline-failure reproductions are in `evidence/regressions-before.log` in the delivery. Other new tests validate new contracts or inspected failure boundaries; they are not falsely counted as reproduced baseline exploits.

## 4. Executed validation

Actual isolated toolchain: **Go 1.27.0, Node 22.16.0**, baseline locked Go/npm dependencies. The baseline commit's own documentation mentioned Go 1.27.1; that was its integrator's environment, not this audit's SDK.

| Check | Outcome/scope |
|---|---|
| Baseline Go race suite | PASS: 158 top-level tests, 216 pass events including subtests, 18 tested packages |
| Final Go race suite | PASS: 178 top-level tests, 239 pass events including subtests, 18 tested packages |
| Go vet / module verification | PASS / PASS |
| Node frontend suite | PASS: 57 tests, including behavior tests and existing source-contract checks |
| TypeScript | `tsc --noEmit` PASS; no separate `vue-tsc` claim |
| Production Vue/Vite | PASS |
| Linux amd64 binaries | All four commands built |
| Windows amd64 binaries | All four cross-built; native execution NOT RUN |
| Browser scenario syntax | Python compile PASS |
| New browser execution | BLOCKED: `ERR_BLOCKED_BY_ADMINISTRATOR` on isolated HTTPS loopback before login |
| Expanded browser scenarios | Included for CI, NOT RUN here; synthetic fixture only |
| Patch/source reproduction | Independently verified by delivery packaging; see manifest and reproduction report |
| Native systemd/SCM boot, installer privileges, power loss, supervisor replacement | NOT RUN; incomplete portions remain |
| Full v3 battery, 50-agent/1000-check capacity, 24-hour soak | NOT RUN |
| Production installation | NOT ACCESSED OR MODIFIED |

Builds used `make dist -o ui` only after separately building the production UI using locked installed dependencies. The generated binaries are build checks, not signed production release artifacts. Their build metadata references the isolated local source baseline, not a newly published remote commit; production binaries must be rebuilt after integration with correct version/commit metadata.

The failed browser attempt is preserved. The final script subsequently gained explicit rules, maintenance, admission/reauth, unread and password scenarios; only its syntax was checked locally. The earlier commit's 14/14 browser result does NOT validate these new changes. No screenshot or visual approval is claimed for this version.

## 5. Integration and rollback notes

1. Verify the package and exact base. Preserve concurrent work; do not force-reset a dirty checkout. Apply one full v6 patch to `e771f35` or deliberately merge and retest later commits.
2. Back up the complete protected controller directory correctly, including SQLite committed state, TLS/CA, encryption/update trust keys, configuration and operation state. A DB-only copy is not a verified controller restore. Test schema/index changes on a populated copy.
3. Build matched UI and server first, then a compatible agent where automatic admission is used. Existing registrations must not be recreated. Keep the actual persisted controller URL; never replace it with the bootstrap default.
4. On the first upgraded server, unknown automatic-profile candidates are blocked until an owner opens the admission window. Already pending candidates and manual enrollment codes continue. Confirm this behavior in the release note to prevent false network diagnosis.
5. Validate Settings rules on a disposable agent; observe a real sustained breach, recovery hysteresis and `policy_changed`. Validate maintenance start/end/cancel while measurements continue. Test password change with two independent browser sessions.
6. Run the expanded browser fixture, inspect screenshots/console and perform real native smoke tests separately. Do not weaken an assertion to achieve a green fixture.
7. Downgrading the server to a pre-v6 binary ignores admission windows, user rules and maintenance semantics. Do not run an old server against new expectations and assume they are enforced; restore/upgrade using a reviewed plan. Retain protected data and journal evidence.
8. A source commit is not permission to restart the owner's live controller. Deployment remains an explicit operator step.

## 6. Remaining release blockers / useful next work

Independent native recovery for service-host self-update, least-privilege Linux/Windows installation and restart evidence, immutable release catalogue and cohort resume, full protected backup/restore with stale-job reconciliation, complete migration time/cancellation/retirement boundaries, long-term aggregates and measured capacity remain open. `operation.retry_selected`, `update.resume` and unsafe supervisor self-update remain rejected.

Rules are now real but global/three metrics only. Extend through versioned profiles and actual evaluator tests, not by accepting unused fields. Maintenance preserves observations; advanced scheduling and complete historical coverage remain separate. Add searchable/paginated maintenance audit if the bounded 200-window list becomes insufficient. Secret cache/offline-restart, TUF root rotation and native local IPC deserve the next reliability pass before broad fleet deployment.

The updated `RELEASE_COMPLETION_PLAN.md` remains the route to acceptance. This round improves several complete vertical slices; it does not turn the entire backlog into completed work.

## 7. Primary design references

These sources explain constraints, not certify this code. Product defaults and test results above come from the reviewed implementation.

- OWASP Authentication Cheat Sheet: `https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html`
- W3C ARIA APG modal dialog pattern: `https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/`
- W3C H102 native dialog technique: `https://www.w3.org/WAI/WCAG22/Techniques/html/H102`
- Prometheus Alertmanager concepts (silencing versus actual alert state): `https://prometheus.io/docs/alerting/latest/alertmanager/`
