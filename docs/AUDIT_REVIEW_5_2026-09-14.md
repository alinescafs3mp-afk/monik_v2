# Monik source review 5: overview polish, visible request editing, and agent arrival

Date: 2026-09-14. Exact reviewed baseline: `37dc23405606b24a82a629533311c19dae577a2a`; tree `a3e00db6ddefa2fb2c2751e022d6a1cd1b2be7a9`.

This is a source correction and bounded test report. The owner's running installation was NOT accessed, restarted, re-enrolled or modified. The full release is still PRE-RELEASE. Prior audit documents describe prior source versions and are not evidence for this patch. Do not confuse this archive with a signed production release.

## 1. Owner requirements delivered

| Request | Actual change | Evidence boundary |
|---|---|---|
| Compact header | Page title immediately after navigation toggle, then Operations and search; account aligned with logout on the right | Source wiring tests and production build; current browser blocked |
| Remove noise | Overview instructional sentence and permanent happy live subtitle removed; actual connection failure remains visible | Static UI contract tests |
| Left-aligned overview totals | Short `На связи`; service, unread incident and attention counters grouped on the left | API regression + UI source checks |
| Platform under machine name | Block/column label below the name in compact rows and cards | CSS/source; visual acceptance pending |
| Acknowledged incidents not counted | Separate `unread_incidents` count for open, unacknowledged incidents. All-open count and health rollup remain unchanged | Real SQLite/API tests |
| Services grouped by machine | Collapsed, stable-ID machine groups; expand/collapse all, machine/service search, orphan identification and bounded existing summaries | Four grouping behavior tests; browser steps supplied |
| Rename machines | Inline editor in Machines and machine detail, expected-name compare-and-swap, committed feedback | Server/storage tests including telemetry arriving after rename |
| Automatic agent arrival | Trusted discovery profile; locally generated identity/proof; outbound pre-enrollment beacons; owner pending list, approve/reject; normal collection only after approval | Actual local HTTPS agent/setup/server/storage integration, not native service installation |
| Configure-request click appears dead | Explicit component opening, correct selected service, scroll/focus, repeat same-service clicks and deep links | Source contracts + expanded browser test; live visual execution blocked |

Showing/hiding a machine in Overview still does not pause monitoring. Acknowledging a critical incident does not change its health or recovery evidence. Returning to a new incident after actual recovery produces an independent unread incident through the existing lifecycle.

## 2. Confirmed defects and fixes

### A. Owner names could be overwritten every report
`internal/storage/store.go: TouchAgent` preferred a nonempty agent-supplied display name over the stored owner label. The new conditional preserves an existing label and still refreshes the observed hostname. `RenameAgent` checks the expected visible name when supplied, trims and validates user input at the API, and does not change identity, URL, enrollment, history or OS hostname.

A test copied into the exact baseline fails with the owner name replaced. The same test passes after this patch. Concurrent stale edits conflict rather than silently overwrite; this is name-value CAS, not a new monotonic metadata-version system. An ABA sequence that returns to exactly the old name is not distinguished.

### B. Repeated acknowledgement rewrote the first acknowledgement
`AckIncident` now retains the original non-null acknowledgement time and actor. It remains an acknowledgement, not incident closure. A second regression fails on the exact baseline and passes on this source. Overview reads a separate unread count instead of filtering away actual health evidence.

### C. A route change was not a visible editor action
The previous request-configuration link could navigate to a page whose Services tab/editor was below the viewport; same-route clicks did not necessarily reopen a hidden tab. Within a machine it now emits a typed configure event; the page opens the editor, selects the intended immutable service ID, switches the tab, scrolls, focuses its heading and preserves route state. Direct links carry the editor anchor. A dirty draft is not silently overwritten when changing services. A second click on the same service does not require a URL change.

The machine detail request now publishes independently from history/point reads. A failed graph query shows a graph error and does not hide the service editor or machine inventory. The browser scenario includes the same-route repeat and an injected graph failure. Its syntax/build passed; execution here stopped before login, so no visual-success claim is made.

### D. Setup could create competing identities
A process-lifetime OS file lock serializes ordinary enrollment and new discovery preparation. The lock inode is retained, with the OS releasing the lock when its process exits. Automatic discovery uses one `agent.json` in one dedicated state directory. Alternate config paths and attempts to mix code enrollment into that discovery directory are rejected before overwriting credentials. Existing configs and ordinary interrupted enrollment intents remain protected. This is not full transactional native installation recovery.

### E. New arrival must not mean automatic trust
Public arrival is quarantined. It cannot ingest telemetry, read jobs, fetch secrets, rename existing agents, or create current healthy state before the owner authorizes that exact proof. No reused global bearer token is distributed. Self-reported hostname/OS are visibly untrusted metadata. Fingerprints must be compared with the install/run output, not merely recognized by hostname.

## 3. Automatic arrival protocol and limits

The owner downloads a non-secret profile from Add machine containing the actual controller URL, CA PEM and `auto_discover: true`. Each installation generates and persists an independent 32-byte random secret and agent ID. Setup itself sends nothing; `monik-agent run` and the installed service launch the waiting loop. Beacons go only to the configured HTTPS controller, with certificate/name verification and no redirects or implicit proxy.

Normal pending interval is 30 seconds plus 0-5 seconds of identity jitter; failures back off to 60 seconds plus jitter; a rejected device waits 10 minutes plus jitter. Each exchange has a 10-second timeout and a bounded response. Only metadata is sent while waiting, not host metrics, listener inventory or service responses. The waiting phase persists across process restarts.

The server requires completed local owner setup. Announcements have a 4 KiB body limit, strict fields, validated proof/metadata, a maximum of 100 retained candidates, 100 per source IP, a fixed seven-day lifetime, a peer limit of 300/minute and a global limit of 600/minute. These are bounded small-fleet defaults, not DDoS guarantees or a measured capacity claim. Existing pending polls do not continually generate audit events. Public clients cannot list candidates or approve themselves.

Approval and initial agent/config-revision creation are transactional. Approval requires the exact fingerprint and a signal no older than two minutes, plus an owner session, CSRF and recent authentication. Rejection has the same authorization. Repeated identical decisions are idempotent while the decision record remains; opposite decisions conflict. The existing operation key reconciles a lost administrator response even after the candidate record has been retired.

The agent must confirm the same ID/proof and preserve its chosen URL/CA before it atomically leaves quarantine. It does not pretend the returned desired revision has already been applied. A real committed live report is still needed for online status. Approval does NOT pin the machine to Overview. An already revoked identity is not re-enrolled by repeating its announcement. A rejection is a seven-day quarantine tombstone, not a perpetual device-ban system; after expiration it may request approval again but cannot become authorized without another owner decision.

## 4. Validation actually executed

Environment: Go 1.27.0, Node 22.16.0, existing locked Go/npm dependency snapshot. No new external runtime dependencies. Logs retain their actual timestamps. Local build commit `17cb8d7` is an audit snapshot seed, NOT an upstream release identity; rebuild final release binaries with the actual integrated commit.

| Check | Result / scope |
|---|---|
| Exact source baseline | Tree matched `a3e00db6ddefa2fb2c2751e022d6a1cd1b2be7a9` |
| Baseline Go suite | PASS before new tests; not sufficient coverage by itself |
| Before-fix regressions | 2 intentional failures on baseline: name overwritten; acknowledgement time rewritten |
| Corrected Go race suite | PASS: 158 top-level tests, 216 test/subtest pass events, 18 tested packages |
| Go vet | PASS |
| Frontend Node suite | PASS: 47 tests; includes eight new grouping/wiring tests, of which four check actual grouping behavior and four check source contracts |
| TypeScript | PASS: tsc --noEmit; NOT a separate vue-tsc/component-type audit |
| Production Vue/Vite build | PASS |
| Linux amd64 | All four commands built, not installed as services here |
| Windows amd64 | All four commands cross-compiled, native execution NOT RUN |
| New registration journey | Actual temporary HTTPS server, authenticated approval API, real SQLite and persisted client state; pending report rejected, approved live report accepted |
| Browser script syntax | PASS |
| Current browser walkthrough | BLOCKED before login: ERR_BLOCKED_BY_ADMINISTRATOR on the isolated loopback fixture; passed scenarios: zero |
| Native setup/systemd/SCM/boot/power loss | NOT RUN |
| Full v3 release battery, fleet load and 24h soak | NOT RUN |

The first combined dist attempt timed out while beginning Windows compilation. A separate Windows build and then the complete warmed `make dist -o ui` both succeeded. The UI was actually built first; this flag did not substitute stale UI. The final successful log is included. No built executable in this package is represented as a signed update artifact.

## 5. Boundaries still requiring implementation/acceptance

1. Native installer completeness remains important: Linux's existing installer does not create its `monik` account or assign root-created state ownership to it; Windows's configured LocalService intention is not applied as a complete restricted-identity/ACL policy. This patch adds the worker's pre-enrollment run path, not those native provisioning operations. On a clean host, resolve these prerequisites and prove actual service startup before claiming install-and-forget. Do not silently switch every service to root/LocalSystem merely to hide permissions failures.
2. Service-host independent self-update recovery, immutable release publication, canary/batch orchestration, full-controller protected restore and newer-agent reconciliation remain mandatory release blockers. The prior fail-closed actions remain closed.
3. Native rollout/restore of the pending-registration state must preserve the credential and agent ID. Do not deploy a pre-Audit5 worker to a quarantined installation: it does not know the new waiting state. Existing ordinary registrations remain compatible.
4. The public pending queue is bounded but can still be filled by unwanted callers. Add operator-controlled enrollment windows/allowlists, useful saturation diagnostics, and a quota policy for larger NATed fleets before general public deployment. A known IP/name is not proof that a device belongs to the owner.
5. Browser visual acceptance remains outstanding. Verify header wrapping at narrow widths, 200% zoom, long names/fingerprints, keyboard disclosure/focus and reauthentication. The compiled UI and source-contract tests are not screenshots.
6. Complete long-term aggregates, actual custom-rule and maintenance evaluation, full historical metadata versions, secret offline availability, per-check trust and measured scheduling fairness. No extra cloud dependency, Telegram, remote shell, arbitrary local file reader, machine reboot or application remediation was introduced.

## 6. Delivery and deployment

Apply only the Audit5 patch to `37dc23405606b24a82a629533311c19dae577a2a` or integrate changes deliberately when main advanced. Preserve concurrent edits and all protected production configuration. Rebuild UI before Go server. Upgrade controller first so its routes and approval UI exist, then upgrade selected agents. Existing enrolled machines must NOT be re-enrolled or reset. Use the advertised URL only when generating a new profile; preserve the actual deployed endpoint on current agents.

Validate a disposable foreground pair first, then native installation on one real test host. Confirm waiting/approval, first live report, rename surviving future reports/restart, unpin without pause, read acknowledgement without recovery, grouped services and repeated editor opening. Retain the two reproducible regression tests. Record real screenshots/CI run IDs and native results separately from this source report.
