# V21 security acceptance and boundaries

Base: `3303e991a2624c945662355e22fa7cfb8ef06a6a`. No live credentials or endpoints were exercised. All negative tests use temporary SQLite, loopback HTTP(S) or in-process handlers.

| Boundary | Required behavior | Evidence |
|---|---|---|
| Viewer self-logout | Own session removed, secure cookie expired; other session unaffected | New HTTP router test |
| Viewer mutation exception | Only exact POST /api/v1/logout; CSRF/origin remain; other POSTs rejected | New negative router matrix |
| Logout write failure | Error, no false success and no misleading cookie deletion | SQLite trigger failure |
| Export after delayed auth | Revoked, expired or demoted snapshot cannot release export data | Existing middleware plus real export/temporary DB |
| Export missing vs unreadable | DB failure is 503, not false 404 | Closed-store test |
| Pending credential creation | Still-current token and non-revoked agent checked in transaction | Delayed-body HTTP test and direct stale-snapshot transaction tests |
| Concurrent candidates | One unexpired candidate cannot be overwritten by another request | Two simultaneous registrations |
| Identical registration retry | Same verifier/job returns original expiry, does not extend it | Fake-clock storage test |
| Credential body | Nonempty bounded job reference and canonical SHA256 hex | Negative HTTP tests |
| Normal rotation | Register, repeat, promote, already-current handling; old token denied | Positive server integration test |
| Failed candidate write | No pending row or success | SQLite insert failure |
| TV local choice | No server profile POST, no API authority, no shared hiding | Actual composable tests and new browser scenario |
| Clean screen | Warnings/exit/paging reachable above rows; no footer | Compiled Vue template SSR and browser scenario |
| Action icons | Only native links to existing routes; named, focusable, square | SSR, CSS/geometry contract; real browser remains required |

## Scope and non-claims

These are concrete correctness fixes, not proof of immunity to intrusion. The export change concerns requests that had already authenticated before their authority changed; it is not a demonstrated anonymous download bypass. A delayed rotation request initially possessed a valid key for its own agent. The fix prevents that stale request staging a successor after revocation or rotation; it does not declare the entire credential protocol formally verified.

The conditional registration preserves existing protocol semantics and does not add a new command-approval system. Previously available worker/SSH console authority remains unchanged. The low-level storage setter is retained for trusted tests, not used for HTTP registration. Data already transmitted cannot be recalled by a later logout.

Clean TV is presentation only. It is not a kiosk, a restricted role, a session extension, a passwordless console or a new inbound port. Continue requiring owner/recent-auth for authorized terminals and explicit local agent console opt-in.

No current dependency-CVE inventory, independent external perimeter test, native installation/boot, full protected restore, service-host self-recovery, physical TV or 24-hour load test is claimed. The root-only UID fixture is confined to disposable processes and directories; it does not prove every systemd sandbox directive on the user's OS.

Primary-source design context consulted 2026-09-16: MDN Navigator.userAgent (unreliable identity inference) and OWASP Session Management Cheat Sheet (visible logout and server-side invalidation). Links are in evidence/reference-sources.txt. Test results come from this source tree, not those documents.
