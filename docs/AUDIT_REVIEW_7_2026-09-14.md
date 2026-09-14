# Monik Audit 7: selective monitoring, wallboard and bounded remembered login

Date: 2026-09-14. Reviewed integration baseline: `5d17aaab6b3f4259820b089c28a7f4367e065372`; tree `1c2aaf9da364c541471bb45b9ec43bf3329a65d6`.

**Source corrections and implementation, not a deployment or a certified release.** The owner's installation and television were not accessed. Prior audit reports are historical. This review preserves the user's existing identity, controller URL, checks, request bodies and secrets. It does not declare the outstanding native/recovery requirements complete.

## 1. Scope actually completed

### Independent service monitoring and presentation

Two separate controls are now wired end to end: **Check** changes an existing primary check's `paused` state through `check.apply`, preserving all other fields and using the actual `desired_config` DTO plus base revision; **In overview** changes only the service's persisted `pinned` flag through a server-side compare-and-swap. Preference completion never waits for a nonexistent agent effect. The overview filters services by that flag instead of ignoring it.

Each selected machine contains only pinned, non-hidden services. The machine itself must also be pinned. Not pinning a monitored service does not mute incidents: failures outside the visible list are counted separately, including hidden services. Deliberately paused/inventory-only services and configuration progress do not become fabricated service failures. Existing incidents remain unresolved until their real lifecycle supplies a reason; pause is not recovery.

A machine-level policy can pause all service checks or re-enable all nonignored checks while leaving host collection and the control channel active. Explicitly enabling an individually ignored check clears its ignored state; bulk enable preserves ignore exclusions. The UI states desired versus confirmed state, does not claim network-wide atomicity, and warns about an already running bounded request.

New services default to a paused primary definition, unless `auto_monitor_new` is explicitly enabled. Existing configured checks are not modified by the new default or rediscovery. Initial listener identification still uses small bounded HTTP/TLS probes to determine whether an unfamiliar socket is a web service. With the default policy, the additional common-health-path advisor is not run. Once a service is disabled and the agent applies that revision, subsequent inventory passes enumerate its listener but do not send HTTP/TLS/health-advice requests. An unresolved inventory candidate says why it was not probed; it is not fabricated HTTP evidence.

Suppressions include the ORIGINAL discovered socket as well as a custom check's dial target. This matters when the owner changed a check to an alias or another local port. Exact normalized socket exclusions are not new probe permissions. Several virtual hosts on one socket may conservatively suppress discovery of that socket; explicitly enabled periodic checks remain independent. In-flight discovery/check work may complete within its existing bounded timeout. Global host pause already suppresses launching discovery.

The upgraded worker advertises `selective_monitor_v1`. Only upgraded workers receive the additional original-socket exclusions. Enabling automatic future monitoring requires that capability to avoid an old worker ignoring a new field yet claiming a matching configuration. Default-false/empty additions use `omitempty`, preserving hashes of old configurations. Server/UI first, then workers, then enable optional new policies. Old workers may still probe during rediscovery despite an ordinary per-check pause; the UI explicitly calls out the required upgrade.

Limit: the inherited primary-definition budget is still 128 per agent, including disabled definitions. This is not unlimited fleet capacity or multi-check-per-service support. Increasing that limit without measuring the scheduler/ingestion/storage path is not part of this change.

### TV and mobile presentation

Explicit modes: Auto, Compact and TV. Mode and TV density are device preferences, not agent configuration. An explicit `?display=tv` link is supported; later manual changes update that parameter so reloading does not undo the choice. Failed local storage is visible but does not break the UI.

TV has a separate bounded row layout, no sidebar by default, full available page width, smaller chrome, three tested numeric font settings (12/14/16 CSS px), persistent density, and a page capacity computed from the actual viewport/row height. Previous/next always remain available. Optional 20-second page cycling pauses on focus/hover/hidden document. No browser zoom dependency, transform scaling, disabled pinch zoom, CDN or external resource is introduced. Fullscreen is optional and errors explain that the layout still works without it.

Each TV row shows identity/contact, CPU, RAM, disk, ping, two selected service summaries and an explicit count for additional selected services/hidden problems. Text can truncate with a title; full details stay available. Service freshness advances locally even when no new server event arrives. The former 860px responsive breakpoint no longer forces TV rows into the tall mobile card layout. Under 600px the TV view degrades to a smaller grid rather than demanding unreadable fixed columns.

Mobile adjustments bound flex/grid children, preserve 44px controls at narrow sizes, allow horizontal scrolling inside true data tables rather than the entire page, and retain the existing keyboard/focus behavior. Mobile and TV are deliberately not inferred from User-Agent or a 4K panel's physical resolution. A TV can report a small CSS viewport.

**Physical Yandex/TV behavior and screenshots are not validated in this environment.** The CSS/logic/build tests and expanded browser scenario are evidence of specific portions, not a claim of real remote-control usability. There is no new viewer-only server authorization boundary: TV mode remains an authenticated owner session. Use remembered owner login only on a trusted private device. Read-only wallboard credentials are a remaining security/usability requirement for public/shared displays.

### Login and session lifecycle

The login form has an accessible eye button, non-submitting toggle, consistent current-password/username autocomplete, caret/focus preservation, hidden-on-background behavior and a disabled duplicate submit. It clears the password after successful login. It does not write a password or session token into localStorage/sessionStorage. The native browser password manager may separately offer to save the password; the application cannot force that decision.

“Remember login for 30 days” is opt-in and unchecked by default. It creates a high-entropy server-backed session with an absolute 30-day expiry and a persistent HttpOnly/SameSite=Strict cookie, Secure on HTTPS including the configured HTTPS deployment behind a proxy. Without the checkbox: a nonpersistent browser cookie and a 12-hour server lifetime. Browser session-restore features can restore a nonpersistent cookie, so do not promise that closing a particular browser revokes the server session. Server expiry, explicit logout or revocation are authoritative. There is no infinite sliding extension.

The settings page lists bounded own-session creation/expiry/current metadata and can revoke all other browser sessions with recent authentication. No session IDs, hashes, cookies or CSRF material are returned by the list. Revocation verifies that the retained session belongs to the actor and is active, in the same transaction. Logout now propagates persistence failure rather than falsely claiming revocation. Password rotation still revokes all sessions; agent credentials are untouched. TV mode does not weaken CSRF, recent-auth or password-change requirements.

## 2. Audit findings and fixes

| Finding | Correction / evidence |
|---|---|
| Service pin existed in storage but overview disregarded it | Real filter and dedicated CAS storage function, missing-ID/type tests, overview tests |
| Pinning and monitoring were easy to conflate | Separate controls/actions/acknowledgements; pin changes no desired revision/hash |
| New discovery automatically started every check | Default paused new definition plus explicit future-service policy; existing checks preserved |
| Paused checks could still be probed by discovery | Exact socket suppression before network identification/advice, including original listener; real local HTTP counter remains zero across repeated passes |
| Hidden monitored failures could escape the machine summary | Failure roll-up is independent of presentation visibility; regression test |
| Compact overview was still tall on narrow TV viewports | Dedicated layout and bounded paging, rather than CSS zoom or misleading UA detection |
| Browser reload could restore the old TV query mode over a manual choice | Update only the explicit display query parameter when mode changes |
| Login repeatedly required entry after browser restart | Explicit bounded persistent session, not plaintext password storage |
| Logout could claim success after a database failure | Error retained in API/UI; do not clear local UI as successful logout when revocation failed |
| Retention test coupled volume correctness to a five-second deadline | Separate deterministic bounded-volume execution from canceled/time-budget behavior; interleave tables; own cleanup deadline yields and diagnostics show remaining lag |

The retention change does not promise a hard real-time five-second upper bound under stalled disk I/O. SQLite/context interruption and statement completion can be delayed. Non-budget database errors still surface. The volume limit remains ten batches of 1,000 per table; new tests did not delete the limit or weaken the numeric assertions.

## 3. Validation and limitations

See `docs/audit/validation-review7-2026-09-14.json` for final counts and exact command results. The baseline race test failed on retention timing under resource contention. An initial full parallel validation was interrupted by the execution timeout; it is NOT counted as a passing run. A fresh serialized package-level race run is the final gate. Go 1.27.0 and Node 22.16.0 used the reviewed lockfiles from the existing toolchain snapshot.

New tests include real temporary SQLite and HTTP handlers, remembered/nonremembered cookies and absolute expiry, revocation scope, strict flags and CAS, service defaults, original socket suppression, and a local listener with zero network requests while disabled. Node tests include pure behavior and clearly identified source-contract tests. TypeScript `tsc --noEmit` and Vue/Vite build are separate from full `vue-tsc` and browser acceptance.

The expanded Playwright scenario covers pin/unpin, actual persisted monitoring state, password visibility, persistent cookies, two rows fitting a 960x540 CSS viewport, display preference reload, 1920x1080 screenshot and prior mobile/custom-check/history/reauth flows. It uses real compiled API/UI but synthetic metrics. The attempted browser navigation was blocked before login with `ERR_BLOCKED_BY_ADMINISTRATOR`. Do not inherit the baseline's 19/19 pass as proof of this new scenario. No alternate path was used to bypass that restriction.

No native systemd/SCM installation, service-host independent recovery, actual TV/Yandex, 50-agent load, 24-hour soak, full-controller restore or complete v3 acceptance is claimed. Cross-builds are not native executions. No signed production binaries are shipped in the corrective source package.

## 4. Integration and rollback

1. Use a clean integration worktree based on the pinned commit; retain intervening changes. Apply the single supplied patch once, or use the source ZIP only as a reference. Verify checksums and reproduce the tested tree on the exact baseline.
2. Build matching UI before server. Run tests/vet/typecheck/build and the browser scenario in the existing CI environment. Inspect actual outputs. Do not weaken assertions or substitute old screenshots.
3. Back up the protected controller and selected agent state with their real runtime paths. Update server/UI, then one worker, then verify capability and desired/applied hashes before the fleet. Preserve the selected endpoint; never reset to the bootstrap IP.
4. Existing pins remain persisted. Previously displayed but unpinned services disappear from the overview by design, with a visible selection prompt. Select the desired services. New discovered definitions are paused by default, but existing enabled checks remain enabled. Use the explicit per-machine bulk pause to change those.
5. To stop probes, wait for the upgraded worker's applied revision/hash. Already executing requests finish by their normal timeout. A disconnected agent cannot obey an undelivered pause. Manual trial is an explicit separate operation and can still send its approved request.
6. Downgrading to a pre-v7 server restores its old new-service/overview semantics. An older worker cannot enforce all original-socket exclusions or auto-monitor fields. Do not combine a downgrade with enabled new fields and infer acceptance from a raw version string. Retain new and legacy queue/config files per the prior rollback runbook.
7. A remembered device keeps owner authority until expiry/logout/revocation. Lost/shared devices require revocation or password rotation. Cookie storage purges, private mode or device clock issues may require signing in again; native password saving depends on that browser.

## 5. Completion priority, not another feature expansion

This closes the service-selection/display/login slice; it does NOT close the whole release. Before another decorative round, finish native user/ACL ownership and independent service-host recovery, immutable signed release publication with durable batches/resume, protected full-controller backup/restore plus stale-job reconciliation, and long retention/coverage. These are unfinished implementations, not merely unavailable test devices. Keep the explicit fail-closed boundaries until behavior and native evidence exist.

Primary constraints: OWASP Session Management Cheat Sheet (`https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html`); MDN HTML autocomplete (`https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Attributes/autocomplete`); MDN text-size-adjust (`https://developer.mozilla.org/en-US/docs/Web/CSS/text-size-adjust`). These sources inform browser/security constraints, not validation of Monik.
