# Monik source audit, second corrective pass

Date: 2026-09-13. Baseline: `2f63c0b59498beb9436c092554cfef10e1942489` (tree `c1ac8f958dca5ad11819bc0d5e7e97244ed5198b`).

**This report describes the accompanying source changes, not the owner's running installation. Production was not contacted, restarted or modified. The project is still pre-release.** Compilation and unit tests do not establish native fleet readiness. This report supersedes contradictory completion claims in the baseline status document, not the owner's requirements.

## 1. Delivered user-visible changes

- Overview defaults to compact horizontal machine rows: identity/freshness, CPU, RAM percentage and bytes, most-filled local filesystem and mount, rolling mean ping/loss, and concise service outcomes. Cards remain an optional remembered layout. Failed services sort first without mutating received data. Additional services have an explicit detail link.
- Machines has a durable **Show in overview** checkbox. Only checked machines appear in Overview. This is an owner preference, not pause, ignore, mute or agent configuration. Saving waits for the server's committed result, shows progress, restores the actual state after an error and uses the existing lost-response/idempotency reconciliation. A new installation with no pins has an actionable empty overview; there is no silent auto-selection of every host.
- The sidebar collapses to an accessible icon rail on desktop and remembers the preference. Mobile navigation closes on route changes/Escape. Toggle state, titles, labels, focus return and a skip link are present. Local-storage failure does not crash navigation.
- Machine charts have permanently visible numeric/value ticks, units, gridlines and time ticks. Percent axes cover 0..100; other axes use tested sensible steps, including constant/empty/tiny ranges. Dates appear across midnight and the browser's time zone is explicit. Ticks adapt to narrow widths. The selected absolute range defines the axis even if samples cover only part of it. All six 1/2/3/6/12/24-hour ranges remain. Mouse and keyboard select measurements; min/max excursions and gaps remain visible.
- Service feedback is structured and bounded: actual method, HTTP code/status text, response duration, content type, sampled-body state and narrowly recognized health fields. GET bodies are read under the existing size/time budget, not copied into telemetry. JSON status/health/state/ready/ok/success and tiny plain health words are recognized; negative contradictory fields take priority. Application health is still `not_configured` until an actual expectation is configured. A self-reported `ok` is evidence, not proof of dependencies or business correctness.
- An optional service disclosure shows this metadata without a raw-body preview. No arbitrary HTML is executed or stored. HTML, empty, malformed, limited and HEAD-only replies are distinguished. Existing HEAD checks are preserved; edit them to GET to inspect safe body feedback. Newly discovered baseline checks use GET. HEAD 405/501 falls back to a bounded GET within the same total deadline.
- The check editor now supplies the required supported interval/timeout, uses the health path, preserves check identity on subsequent saves, carries the base configuration revision and exposes trial/expectation fields. A trial runs on the agent under its probe policy, not on the controller.

## 2. Confirmed defects and corrective boundaries

| Area | Baseline defect | Correction and evidence |
|---|---|---|
| Signed updates | Individually signed metadata could be inconsistent; a candidate binary was not checked against the authenticated target record | Validate timestamp/snapshot/targets version, byte length and hashes, reject corrupt high-water state, and bind the exact OS/architecture worker bytes to the signed target entry. A stale-target/fresh-snapshot regression failed before this fix. Invalid target bytes are rejected before replacing published files, enrolling initial trust or advancing version state. Signed bytes remain separate from native install acceptance. |
| Update activation | Target copies and rollback could damage the only working file; journals/worker sessions were insufficient proof of completion | Atomic staged copies, confinement including symlink resolution, digest-checked previous-good rollback, serialized supervisor operations, and journal-confirmed new-session evidence. Update failure with successful rollback is `rolled_back`, not success. |
| Service-host self-update | Replacing the running supervisor's file was treated as a verified supervisor upgrade | Explicitly rejected until an independent native recovery/install mechanism exists. **Mandatory release feature remains incomplete.** A running old supervisor is not proof the replacement can boot. |
| Rebind | Activation could be reported before fresh contact on the new route; armed primary-loss behavior and fallback were incomplete | Persisted candidate trial, previous URL/trust, three committed fresh exchanges over at least 15 seconds, 60-second trial fallback, local primary-loss arming, monotonic generation, status reports and strict retirement. Failed/unverified candidates never become confirmed. |
| Trust retirement | Any remaining root, even an unused spare, could justify removing the active root | Test the actual current controller with the proposed remaining bundle before saving it. Trust/config changes use the persisted configuration rather than an ambiguous stale sidecar. |
| Configuration durability | Effective configuration and revision lived in separate publications | Authoritative revision/hash/body envelope is atomic and validated. Preserve the legacy body file for rollback to the old worker. Detect/adopt a later configuration written by an older rolled-back worker only after hash validation. Corruption no longer silently restores defaults. |
| Receipt validation | Actual trial/trust/credential operations could be rejected; lifecycle success could lack operation-specific evidence | Verify action-specific stages, identities, versions and new sessions. A failed HTTP trial may be a successfully executed trial, never a healthy service. Persist receipts before removing intents. |
| Configuration API | Lost base-revision checks, incomplete field validation and duplicate service check definitions | Atomic server-side CAS validation across selected targets; strict one-primary-check-per-service, real booleans, nonfractional status codes, supported scheduling values and known checks. Malformed requests cannot silently become default/no-op success. |
| Ping | Unrelated or malformed ICMP input could be counted as a reply; initial interval counts were misleading | Validate peer, reply type/code, sequence, payload and applicable identifier; ignore unrelated packets until deadline; distinguish failure-to-send from loss; bound real observations to a nonoverlapping minute. No external ping was run in this audit. |
| HTTP feedback | Health path/expectations and response interpretation were incomplete | Deadline- and size-bounded sampling, exact field validation, safe health vocabulary, completion timestamp/duration, missing-secret failure and proper fallback evidence. No arbitrary body retention. |
| Install/CLI | Errors could be ignored; self-copy could truncate a binary; Windows service registration used an unsuitable executable/argument boundary | Propagate install/start errors, atomically copy regular files with same-file handling, quote systemd arguments, pass Windows executable/argv separately, update auto-start configuration and check service start. Missing controller subcommand no longer panics; unimplemented recovery no longer claims changes. Native OS acceptance remains open. |
| Historical queries | Certain database errors were returned as an empty success | Preserve actionable failures for point and incident lookup. Chart axes no longer conceal scale/time information. |
| Phantom actions | Some accepted actions saved unused settings or unrelated data rather than performing their advertised effect | Explicitly reject `rule.save`, `maintenance.set`, `history.export`, `operation.retry_selected`, and `update.resume` while their actual evaluator/export/orchestration paths are unfinished. Do not remove these boundaries merely to make buttons green. |

The safe subset was corrected, not replaced by a new framework. Original agent identities, service IDs, public API baseline, five-second defaults, controller URL and data directory conventions are preserved. No automatic production migration/deployment is performed by committing this source.

## 3. Executed validation

Environment: Go 1.27.0, Node 22.16.0, reviewed locked Go/npm dependencies obtained from the repository's audit-tooling artifact. The source snapshot's tree matched the remote baseline exactly. Baseline tests passed; targeted new tests exposed uncovered behavior. Final source results:

| Check | Result |
|---|---|
| `go test -race -count=1 -json ./...` | PASS: **101 top-level tests, 133 test/subtest pass events, 14 tested packages**, no failed tests |
| `go vet ./...` | PASS |
| `cd web && npm test` | PASS: **20 tests**, including 11 new presentation/chart regressions |
| `tsc --noEmit` | PASS; this is not a separate vue-tsc validation |
| Vue/Vite production build | PASS |
| Linux amd64 server, agent, service-host, release tool | PASS: cross-command build, not installation evidence |
| Windows amd64 versions of all four commands | PASS: cross-compilation only |
| Python browser test syntax | PASS |
| Git whitespace check | PASS |
| Actual local browser walkthrough | BLOCKED by browser policy: `ERR_BLOCKED_BY_ADMINISTRATOR` opening the isolated HTTPS loopback fixture |
| Revised browser scenario in remote CI | NOT RUN at report creation; later CI evidence must name its exact commit/run |
| Native Windows SCM/boot, native restart/power loss, full fleet relocation | NOT RUN |
| Native supervisor self-update/independent recovery | NOT IMPLEMENTED / NOT RUN |
| 24-hour soak, 50-agent/1,000-check capacity, complete v3 acceptance | NOT RUN |
| Owner's live machine/services | NOT ACCESSED |

`make dist -o ui` was used only after separately building the production UI, avoiding a second unavailable-network npm install in the isolated test environment. Normal connected builds may use `make all` / `make dist`. No binaries from this audit are presented as signed production releases.

The revised `tests/browser/audit.py` tests real Go API + compiled Vue using **synthetic telemetry**, including pin selection, dense rows, sidebar persistence, chart axes, narrow layouts and lost-response behavior. It never fabricates a completed agent job. Its presence is not execution evidence. The baseline's previously passing CI browser scenario does not validate this revision's new UI.

## 4. Honest release blockers and remaining risks

1. **Managed lifecycle:** independently supervised service-host replacement/rollback, native Linux provisioning/state ownership, Windows restricted identities/ACLs and before-login boot/recovery evidence. Fixing Windows compilation or unit quoting does not complete install-and-forget.
2. **Signed rollout operations:** full persisted canary/OS cohorts, bounded batches, pause/resume, health criteria beyond process liveness, compatible rollback of all state formats, TUF key/root rotation and complete expiry/recovery behavior. Root-private-key custody and signed distribution remain operator responsibilities. Downloads still need measured cancellation/control-channel isolation and deployment-appropriate transfer timeouts. The server still publishes shared mutable metadata/target locations: immutable per-release publication, concurrent-import serialization and all-or-nothing I/O-failure recovery are release blockers. Do not import a new release during an active rollout; queued jobs for older bytes must fail verification rather than execute a replacement.
3. **Rebind edge cases:** native multi-host trials, trustworthy scheduled activation/time uncertainty, disconnected cancellation, complete old-endpoint expiry/retirement policies and physical-controller single-writer handoff. An offline unprepared agent cannot learn a new unknown IP. Preserve accepted generations and cryptographic trust across restore. The implemented 3-exchange/15-second trial is not a full high-availability system.
4. **Operational completeness:** custom rule changes and maintenance must actually feed the evaluator/history; selection retry and update resume need real orchestration; time-range export must export history rather than current incidents. These APIs explicitly fail until completed.
5. **History/storage:** full long-term aggregation and coverage, historical service/check/rule versions and vantage-aware queries, capacity/retention under millions of observations, bounded database maintenance. Recent raw graphs are not proof of 180-day historical acceptance.
6. **Backup/restore:** protected full-controller identity/TLS/keys/configuration backup and restore with stale-job reconciliation; a database-only snapshot is insufficient. Test legacy timestamp migrations on a copy of a populated database.
7. **Service coverage/performance:** virtual hosts, custom secrets, Docker and unsupported OS sensors retain documented gaps. Generic HTTP feedback cannot infer every application's health. Slow collectors/checks, spool backlog, downloads and control exchanges need measured end-to-end scheduling fairness; no guarantee of exact five-second freshness under arbitrary load is made.
8. **Remaining security/lifecycle review:** least-privilege boundaries around local supervisor controls, enrollment response-loss/retry behavior, certificate/root expiry, revocation/rotation failure boundaries, request/diagnostic size limits and real restore fault injection still require native/long-running acceptance. The tested corrections are not a claim that every possible vulnerability has been eliminated.

Telegram remains deferred. No remote shell, host reboot, arbitrary file reader, monitored-application restart or automatic router/firewall changes are added.

## 5. Integration and deployment runbook

- Use a clean worktree. Compare main with the exact reviewed baseline and integrate intervening work rather than force-resetting or copying an archive over a dirty repository. The source ZIP is a reference snapshot; the Git patch is the normal integration path.
- Before deployment, back up the protected controller directory including database/WAL through the proper backup procedure, TLS material, secrets, effective settings and enrollment identity. Also preserve each test agent's protected state, including legacy `applied.json`, the new `applied-envelope.json`, identity and credential files. Never commit those runtime files.
- Build UI before server. Run Go race tests/vet, frontend tests/typecheck/build and native binaries. Run the revised browser CI scenario and inspect actual artifacts. Cross-compilation is not native installation.
- Deploy to a disposable/local test pair first, then one selected real agent through a deliberate operator action. Verify pin persistence, overview rows, no stale-green values, axis labels, expected service responses and desired/applied revisions.
- Validate new signed-update target binding with a correctly signed test release. Retain a known-good executable and protected state snapshot. Do not remove the supervisor-self-update rejection without an independently proven OS recovery path.
- Exercise prepared/armed migration between two aliases of the same test controller, success, timeout and recovery. Do not use two independent writable database copies. Do not automatically change the existing deployment address to a compiled default.
- Published source changes are not automatically deployed by this audit. Re-read this report and the acceptance ledger before declaring a release.

## 6. Primary technical references

- TUF specification: <https://theupdateframework.github.io/specification/latest/>. Metadata linkage, freshness/rollback defenses and authenticated target bytes are separate from native installation.
- W3C disclosure pattern: <https://www.w3.org/WAI/ARIA/apg/patterns/disclosure/>. Toggle state and accessible disclosure behavior.

These references inform constraints; numerical defaults, tests and implemented behavior above come from this source review, not from a claim that the references certify Monik.
