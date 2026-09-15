# Audit 16: V15 first-pilot corrections

Date: 2026-09-15. Baseline `c671137d16269e568061a478b12fe3d4f3e7c82f`. Scope: the owner's reported first-host UI/ping defects, adjacent freshness, polling, ICMP evidence and update-gate regressions. This is a targeted audit plus the complete regression suite, NOT proof every project requirement is complete.

## Confirmed findings and fixes

### A16-01: passive inventory updates stole scroll and focus (high usability impact)
`web/src/components/CheckEditor.vue` watched a getter returning a newly allocated array `[route.query.service, services.value.length]`. Replacing detail on every poll invalidated the computed service list and returned a different array even when its values were equal. Its watcher called `focusEditor`, which calls scrollIntoView and focus. Changing to separate primitive watcher sources makes only explicit navigation or initial availability open the editor. Existing same-service explicit Configure action remains supported.

The new Node regression extracts and executes the exact SFC watcher with real Vue reactivity. Eight passive snapshots caused nine focus/scroll calls on baseline, versus one after correction. Adding an unrelated service also no longer reopens the selected editor. These are two cases of ONE root defect, not two invented independent bugs. Actual browser DOM/scroll acceptance is authored but blocked here.

### A16-02: background network activity drove visible manual refresh state
`usePolling` used one refreshing flag for timer/SSE and operator clicks. New `createPollingController` isolates initial load, internal busy and explicit manual refresh. Periodic hints do not grow a queue behind a slow request; an explicit request gets a coalesced fresh follow-up. Errors stay visible, last content stays mounted, and dispose prevents queued publication. New behavioral tests cover manual/background lifecycle, burst coalescing, errors and disposal.

The machine page no longer fetches expensive history while editing services or agent settings. History-specific controls live on the charts tab. A datetime input being edited is not overwritten by a periodic read. Service editor identity and drafts remain stable.

### A16-03: stale-state policy and inconsistent presentation clocks
Baseline monitoring grace was 15s, not below 10s. The user-visible flicker cannot be assigned conclusively to one live cause without the pilot's timings. Two actionable issues existed: browser wall-clock comparison with server timestamps, and a tight budget spanning check/report/browser polling. Grace is now 20s for fast samples; the server exposes it in DTOs, slower intervals retain three periods, and a monotonic server-anchored presentation clock ages results across screens. Invalid infinite interval input no longer grants unlimited freshness.

IMPORTANT: update rollout observation remains at its original 15s via a separate `RolloutFreshContact`. The first full verification caught the accidental coupling of the new display grace to the rollout freshness test; it was corrected without weakening that test. Unreachable remains 30s. Changed configuration still requires new matching evidence; this fix does not reuse an old result under new expectations.

### A16-04: basic HTTP success displayed as unconfigured/grey
The terse summary no longer adds "здоровье приложения не настроено". `responds` remains an internal truthful distinction from application health but is rendered green. The default success contract is HTTP 200..399. Non-success baseline HTTP receives `http_error` consistently in summary and evaluator. Explicit custom health expectations retain priority, including failed assertions at 200 and an explicitly accepted status outside the default set. Updated the earlier 401 regression expectation to the OWNER'S NEW policy; it still asserts the actual HTTP code is retained and stale evidence is not live.

Paused/inactive/unmonitored entries have a hollow inactive marker. Active stale/unknown/pending data is never green. Read/unread metadata still cannot repair a failed service. Tests cover 200, 204, 302, 401, 403, 404, 503 and custom pass/fail precedence.

### A16-05: ping installation and misleading evidence
Baseline Linux managed unit ran as monik with NoNewPrivileges but supplied no ambient raw-socket capability. Linux ping sockets may already be allowed by the system's ping_group_range; otherwise the raw fallback needs CAP_NET_RAW. Missing permission is therefore a likely, not remotely proven, cause of the owner's missing ping. No production logs or target network was accessed.

New unit generation grants only CAP_NET_RAW in bounding/ambient sets, retaining the monik account and NoNewPrivileges. CAP_NET_RAW is raw-socket permission, NOT a kernel capability limited solely to echo or 8.8.8.8. It is intentionally scoped to the service rather than granting root or changing a system-wide sysctl. A separate explicitly invoked repair script handles an already installed V15 unit. A binary update alone does NOT modify systemd configuration.

The old collector marked an unsent non-permission-error sample as supported with LastSuccess=now. A baseline regression reproduced this false success. The corrected aggregate retains only same-target current-window observations, records last actual reply, and reports pending/disabled/permission_denied/send_failed/no_reply/ok. Unsent samples do not count as packet loss; actual unanswered packets do. Real loopback ICMP attempted here was denied by sandbox socket permissions. Do not claim network reachability proved.

### A16-06: compact layout wasted space for selected services
Facts use bounded, left-anchored tracks. Remaining width holds measured columns of three selected service entries. Capacity is bounded and responsive; overflow is an explicit expansion action, not hidden monitor selection. Mobile layout wraps metric and service groups; TV uses the same service presentation while retaining its independent density/paging. CSS and production build pass, geometry helpers pass. Real screenshot and overlap checks require the included browser scenario on the host. There is no new generated mockup pretending to be deployed UI.

## Tests and honest negative evidence

`frontend-before.log`: baseline scroll/focus reproductions, invalid interval acceptance and the intentional grace-policy boundary (the last is a behavior change, not independent proof of a prior bug).
`ping-before.log`: unsent packet falsely marked successful on the actual baseline collector.
`full-go.jsonl`: intermediate full run detected expected 401 policy mismatch and unintended rollout grace coupling; both resolved before sealed verification.
`build-final.log`: an intermediate compilation caught a wrongly named newly used struct field; fixed and rebuilt in `build-sealed.log`. It is not claimed as a defect in the user's baseline.
`browser-attempt.log`: ERR_BLOCKED_BY_ADMINISTRATOR before login, not a passed browser run.
`kernel-ping.log`: denied ICMP socket creation, not a passed echo.
`go-sealed.jsonl`, `frontend-sealed.log`, `build-sealed.log`, `native-sealed.log` and `validation-summary.json`: final outcomes, including skips and separate process acceptance.

Dependency versions/go.mod/lockfiles were not downgraded or changed. Go 1.27.0 and cached verified modules were used. Exact xterm package archives were checked against lockfile SHA512 before populating missing local dependency directories. `tsc --noEmit` is NOT a separate vue-tsc proof. No vulnerability database scan or native service boot acceptance is claimed.

## Deployment and remaining risks

Run host browser acceptance BEFORE production publication. Deploy a matching built UI/server, then one worker and the explicit service permission update as needed. Check that query editing remains stable through at least four passive inventory reads; explicit Configure still focuses once; background Refresh stays unchanged, manual Refresh still acknowledges the click; loss of connection still ages the data and displays an error. Check 390px mobile and TV at 100% zoom, including long names and many services.

For ping, distinguish local permission from ICMP/network filtering. Confirm the exact service user and actual capability inheritance on the pilot; do not run all agents as root or disable TLS. Existing identity, endpoints, config, journals, service names, watch/pin selections and secrets remain untouched by this patch.

Independent service-host recovery, full protected restore, native Windows SCM/boot, long aggregates, actual fleet acceptance and soak remain open in the completion plan. This package adds no general retry execution or new remote shell authority.

## Primary implementation references

- Linux kernel IP sysctl reference: https://docs.kernel.org/networking/ip-sysctl.html (ping_group_range)
- systemd execution manual: https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html (AmbientCapabilities/CapabilityBoundingSet/NoNewPrivileges)
- Go x/net ICMP API: https://pkg.go.dev/golang.org/x/net/icmp
- Vue watcher source types: https://vuejs.org/guide/essentials/watchers.html
