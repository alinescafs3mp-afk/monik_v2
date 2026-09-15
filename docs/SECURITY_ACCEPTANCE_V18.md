# V18 security acceptance matrix

This is source review plus the recorded tests, NOT an independent penetration test of the owner's VM. All endpoints used in automated integration tests were disposable loopback fixtures. No live controller or machine credentials were used.

| Boundary | Mechanism | Evidence / remaining gate |
|---|---|---|
| Internet -> controller | HTTPS, normal admin-network restriction, owner role, recent auth, CSRF ticket POST | Negative anonymous/CSRF/viewer/stale-auth tests |
| Foreign web page -> existing owner cookie | Exact HTTPS Origin, no URL ticket, first-frame single-use ticket bound to cookie | Wrong origin, suffix origin, null origin and URL query rejected |
| Agent authentication | Current enrollment key, worker session, nonrevoked/current live agent, exact TLS origin and CA | Wrong CA/controller/machine/worker, mixed cookie+agent auth, rotation/revoke tests |
| Agent presence -> shell authority | Presence opens no shell; valid owner ticket and separate local permission required | Opens counter remains zero until permit; failed audit prevents local open |
| Ticket binding/replay | 256-bit random, hash storage, 30s, cookie/agent/channel revision, single consumption | Replay/cross-machine/cross-session/expiry tests |
| Input identity/order | Exact allowed frame keys, type/size checks, seq=last+1, no command/cwd/UID fields | Malformed, duplicate field, unknown target, resize bounds, repeated sequence tests |
| Role/session revocation | Parse actual expiry instants, current DB owner role/user, repeated live checks | Active session deletion/role downgrade; subsecond/offset expiry tests |
| Delayed launch -> revoked session | Recheck after durable intent and after SSH authentication, before launch | Agent audit-trigger revocation regression before/after; existing encrypted SSH tests |
| Output -> browser | Bounded base64/bytes, limited backlog, xterm rendering, OSC52 and link activation disabled | Pure decoder tests, source checks, TLS/PTY round trip; rendered browser NOT RUN |
| Controller -> host privilege | No root network listener; systemd local socket activation -> monik-console, fixed executable | Unit syntax + OS peer-credential/UID tests; actual systemd sandbox and boot NOT RUN |
| Other local UID -> terminal broker | Root parent directory, socket0600 owned by monitoring UID, SO_PEERCRED exact UID | Wrong peer UID denied in actual native subprocess test |
| Shell -> monitoring keys | Separate UID, no extra groups/caps, no_new_privs, private monitor state | Actual mock key unreadable across UIDs; unit namespaces/filesystem sandbox need native pilot |
| Disconnect/shutdown | Cancel both halves, no replay/resume, local PTY close/process-group kill, cgroup KillMode | TLS relay/process cleanup tests; detached descendants under real systemd cgroup still pilot gate |
| Resource abuse | 24 KiB frames, 4096 input bytes, 8192 output bytes, 16MiB total, rate/connection caps, bounded queues/timeouts | Oversize/malformed tests + code review; not a large-scale DoS/load benchmark |
| Telemetry independence | Separate goroutine/channel, no terminal jobs/spool/transcript | WSS test asserts agent_jobs remains empty and audit contains no fixture input; existing native metrics/update tests |
| SSH trust | Protected fixed file, canonical JSON, same-file open check, CAS, pinned fingerprint/algorithm, stale-ticket refusal | Three inherited baseline failures reproduced; setup/enable/disable and SSH tests pass |
| Browser navigation | Request/connection generation + exact machine/transport matching | Source checks; route and actual rendering acceptance delegated to Playwright host |
| Installer authority | Default unchecked explicit local opt-in in owner-prepared matched bundle | Existing installer tests/build and checkbox checks; full clean VM install with console flag NOT RUN |

## Pilot before enabling on the real controller VM

Use a separate disposable Linux/systemd machine first and retain provider console/SSH. Do not test failure injection against the live SQLite or active release store.

1. Install the matched worker/service-host and explicit local permission. Check the installed root-owned unit files and executable, `systemctl cat`, supported sandbox directives and effective runtime properties. Confirm the socket is Unix-only and mode0600 with a root-owned parent; there is no new TCP listener.
2. Open from a recently authenticated owner session. Confirm remote `id`, empty capabilities and `NoNewPrivs: 1`; read a generated protected mock file as the terminal user and expect denial. Do not paste real secret contents into evidence.
3. Verify `ProtectSystem`, `ProtectHome`, private tmp/devices, inaccessible agent state, memory/tasks/fd limits and whole-cgroup cleanup. Unit parsing alone is insufficient.
4. Logout/revoke role/key, stop local socket, restart worker/controller and interrupt network. Confirm session closes, stale tickets cannot reconnect, prior input is never replayed and no shell is created merely by restoring the channel. Deliberately spawned descendants must disappear with the systemd unit; otherwise do not enable on production.
5. Confirm metrics/history/selected probes remain correct during input/output and while channel fails. Verify signed worker update/rollback and how the optional console reconnects without reopening a shell. Never replace service-host remotely via a new ad hoc root command.
6. Reboot the pilot without user login. Verify normal monitoring and optional socket autostart, and reconnect only with a new explicit owner request.
7. Run the full browser harness and use a physical TV remote: short OK to enter width mode, left/right, up/down, OK save, cancel, native +/- clicks, changes survive reload; no hidden Console action or overlap.

## Deployment cautions

- Running the controller or an administrative SSH target as root defeats the limited authority assumption for that component. The new agent terminal does not change the existing server's OS account automatically.
- TLS to controller must remain verified. A reverse proxy must correctly pass original Host/Origin and upgrades and use verified HTTPS upstream; spoofable forwarded headers do not create a trusted cleartext path.
- Owner cookies/passwords on a shared TV remain risky. This package does not deliver a viewer-only kiosk identity or MFA.
- The isolated shell can still run programs, write permitted files and use allowed networking. It is not a read-only policy and cannot guarantee safety from a kernel vulnerability or privileged host misconfiguration.
- Controller compromise permits access to opted-in terminals and existing administrative features. The stream is not encrypted end-to-end against the controller itself.
- No external penetration test, exhaustive supply-chain/CVE scan, native Windows ConPTY/SCM, power-cut recovery, full restore or 24-hour/large-fleet soak was performed. Keep these visibly open.
