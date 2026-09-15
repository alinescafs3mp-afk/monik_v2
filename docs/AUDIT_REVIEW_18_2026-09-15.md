# Monik Audit 18: agent terminal and network authority review

Reviewed base: `afdbbd2573b68d99eb3115032f62f6b33681a5cd`, tree `82fef7293be36b76bb587682425a0ed0685230da`.
Date: 2026-09-15. No production access, live credentials, GitHub writes, service installation, firewall changes or remote commands were performed.

## Delivered scope and important limit

The main feature is a real-time Linux terminal over a separate agent-initiated WSS channel. Browser terminal and direct SSH remain, with an explicit transport selector. A matched worker/service-host build plus local opt-in is required; ordinary agent updates cannot turn on a terminal. The existing one-file installer can carry an explicitly selected console opt-in, unchecked by default.

The implemented agent terminal is deliberately NOT a root/admin shell or an arbitrary selected OS login. A separate `monik-console` account provides a useful unprivileged shell and its own writable home, while protecting monitoring credentials and prohibiting sudo/setuid gains. Direct SSH retains the owner's independently configured administrative login. A systemd service sandbox is not a separate VM or proof against kernel/local privilege-escalation vulnerabilities.

TV remotes now have an explicit OK/arrow adjustment mode, plus clickable native +/- controls that do not require long-press pointer dragging. Existing mouse drag, responsive layouts, saved widths, telemetry refresh and graph-response fixes are preserved. TV console actions have a reserved labelled cell.

## Threat model

Considered adversaries: unauthenticated Internet client; malicious cross-origin page while owner is logged in; stale/expired/revoked owner session; viewer; replayed/altered/cross-machine permit; revoked or replaced agent; malformed or oversized messages; full/slow stream; bad local UID; compromised shell process; tampered local SSH configuration; concurrent shutdown/configuration changes.

Trusted components: the running controller and its protected state, the enrolled agent's private credential/CA, root-managed installed binaries and local systemd policy, the machine kernel and legitimate local administrator. TLS terminates at the controller. This is not end-to-end secrecy from the controller; a compromised controller or stolen CURRENT owner authority can access locally enabled non-root terminals. Local root can already change these controls. This review does not claim an independent penetration test, full dependency-CVE assessment, resistance to every DoS or root/kernel compromise.

## Architecture and fixed authority

- Agent opens only the enrolled HTTPS origin's fixed `/api/v1/agent/console-channel` WSS route. No arbitrary destination/path, proxy environment, TLS bypass or redirect. Enrolled CA/hostname and controller identity are checked. Worker session and CURRENT agent credential are checked by controller before handshake and during the connection. Old credential overlap is not console authority.
- Console traffic is separate from five-second metrics and durable operations. No terminal input, transcript, permit or shell-open message enters the telemetry spool/job table. A channel reconnect opens no shell. Stream close invalidates its agent connection and local session.
- Root-managed Unix socket, not remotely writable AgentConfig, grants local permission. `/run/monik-console` is root-controlled; socket mode is 0600 for `monik`. A per-connection systemd unit runs the helper as a different non-root `monik-console` UID. Kernel SO_PEERCRED verifies the connecting UID.
- Fixed `/bin/sh -i`, native PTY, canonical dimensions, sanitized environment and fixed home. No remote executable, cwd, environment or UID selector. The network relay never runs OS commands on the controller.
- Empty Linux capability sets are checked before shell creation. The helper pins its goroutine to the OS thread while setting no_new_privs and starting the child, avoiding a per-thread guard being lost to scheduler migration. systemd also sets NoNewPrivileges and empty bounding/ambient sets.
- Units declare ProtectSystem/ProtectHome, inaccessible monitoring state, private tmp/devices, restricted namespaces/SUID, task/memory/fd bounds and KillMode=control-group. Native unit sandbox enforcement and descendant/cgroup cleanup MUST be verified on a systemd pilot; syntax and subprocess tests do not prove those OS-manager behaviors.

## Network authorization and lifecycle

Owner-only recent-auth ticket POST uses existing HTTPS session/CSRF validation. WebSocket requires exact same HTTPS Origin, current owner session, and an authenticated first frame; tickets are not put in URL, localStorage or logs. Random 256-bit tickets are stored hashed, expire in 30 seconds and bind the browser session, agent ID and exact live channel revision. Read-only GET and merely opening an agent channel cannot launch a shell.

Ticket consumption, per-machine slot selection and global capacity decisions are serialized. One shell per agent, four active agent terminals per controller, at most eight pending browser sockets, 128 agent channels and 32 unconsumed tickets. Existing SSH limits remain separate. These are defensive caps, not a tested fleet-capacity promise.

Current session expiry/owner role is checked before launch and on input/periodic checks. Revoked machine, credential rotation, changed worker session, lost current telemetry, restore mode, controller shutdown or local socket revocation close the channel. Tests verify session deletion, role downgrade, agent revoke, credential rotation and worker replacement. Live checks normally run every second; blocked writes/handshakes use bounded deadlines. A blackholed network is detected by heartbeat/read deadlines, not magically instantaneously.

Limits: 24 KiB JSON frame, 4096-byte input frame, 8192 decoded output bytes/frame, 16 MiB total output, one hour lifetime, ten minutes without input, bounded queues and browser backlog, input/output frame rate caps. Overflow closes rather than silently dropping/replaying keystrokes. Protocol only accepts input/resize/close with exact next sequence, and strict nonambiguous canonical JSON. The controller applies output validation too. No port-forwarding/tunnel API is added.

Audit intent must commit before sending shell-open. No raw input/output, passwords, private keys or tickets are recorded; channel/open/close metadata only. Close-event writes remain best-effort when storage itself fails. This is not full session recording or a command-history compliance feature.

## Reproduced defects and self-review

The actual base had only the first V17 column variant. Three inherited SSH defects were reproduced in a separate clean baseline before selective integration:

1. Duplicate keys in protected SSH configuration were accepted.
2. A symbolic link was accepted as the protected target file.
3. A ticket could be issued to a browser showing an outdated SSH destination.

All three fail on baseline and pass after fixes. The coherent SSH setup form/hardening is now included, not assumed already deployed. Target settings use strict bounded JSON, fixed file path, compare-and-swap revision, same-file check and independently acknowledged host fingerprint. Target changes invalidate unused tickets and cancel sessions; no automatic fallback to SSH. Native RSA key use selects SHA-2 algorithms. UI configuration never installs sshd or infers a machine's SSH address from the controller URL.

Additional self-review found a session-expiry boundary error in the new/current-session guard: lexical comparison of variable-precision RFC3339Nano text could admit an already expired whole-second timestamp. `selfreview-expiry-before.log` reproduces it, including the exact deadline. The corrected guard parses and compares instants, checks the same user and current role, rejects malformed expiry and bounds the database read. Tests cover subsecond, whole-second and offset representations. Existing generic storage expiry queries were not globally refactored in this feature.

A second deterministic self-review test revoked the owner session during the audit-intent commit. The first draft could send shell-open after that revoke. The corrected code rechecks authorization after the write, and the same test now rejects before local launch. Direct SSH additionally rechecks after authentication and before shell creation.

The first draft of a network test queried a nonexistent table named `jobs`; it was corrected to the real `agent_jobs`. This was a test error, not a product fix. Local npm executable links in the offline test environment were repaired without changing package versions/lockfiles. No failing production test was weakened to claim success.

## Validation

See `evidence/validation-summary.json` for final source counts, exact commands, statuses and evidence inventory.

- Complete Go race suite, vet, module integrity, frontend, TypeScript and Vite build.
- Five shuffled focused security runs, including actual local TLS/WebSocket routing with temporary SQLite, rejection of wrong TLS/controller/worker, cross-origin/cookie injection, CSRF, stale auth, viewer, permit replay/cross-machine/cross-session/expiry, malformed and repeated input, failed audit insertion, revocation and shutdown.
- Full browser-protocol -> controller -> agent-link -> real Linux PTY test, including input/output and `stty size` change. This loopback test does not claim a deployed systemd sandbox.
- Separate native activated-entrypoint test uses actual client UID 1 and broker UID 65534, kernel peer credentials, real shell, mock key mode0600 owned by UID1, no_new_privs and sanitized environment. Wrong expected UID rejects; cleanup joins processes. These generated credentials and UIDs are fixtures, not production accounts.
- `systemd-analyze --man=no verify` passes the generated unit syntax (ExecStart executable existence is substituted with `/bin/true` in the disposable syntax check). No units are installed or run by this test.
- Real compiled Linux worker/supervisor tests retain identity and telemetry across outage/restart, signed worker replacement and failed-candidate recovery.
- Linux/Windows amd64 builds and Linux amd64/arm64 one-file templates. Windows agent-terminal feature explicitly unsupported; successful Windows compilation is not ConPTY/SCM acceptance.
- Mutation fuzz run targets bounded application protocol decoding, not the kernel, TLS library or complete WebSocket implementation.

The auditor's normal Playwright attempt was blocked BEFORE_LOGIN by `ERR_BLOCKED_BY_ADMINISTRATOR`. ZERO browser scenarios passed here. Included new scenarios and existing exact selectors require Grok's permitted environment; physical TV/Yandex and real remote SSH are not emulated. No native service boot, actual socket-activated cgroup kill, power-loss, large-fleet, long soak, complete restore or independent host self-update claim.

## Deployment and remaining gates

Only one local opt-in Linux pilot first, with independent administrator access. Preserve monitored agent rights and state. Never add network-facing root execution or a generic arbitrary-operation runner to work around missing permission. Upgrade the root-owned helper by trusted local administration; self-update stays fail-closed. Rebuild matching installer templates and reload browser tabs after the stricter SSH ticket contract.

Unchanged open product gates: complete protected controller restore, independent service-host recovery/self-update, long-history aggregates, measured fleet/retention limits and native Windows installation/boot. Shared-wallboard viewer credentials remain preferable to unattended owner cookies; they are not implemented here. Agent console is an explicit newly authorized feature, not a blanket weakening of the prior release plan.

## Primary references used during design/review

- OWASP WebSocket Security Cheat Sheet: WSS, exact Origin validation, session revalidation/revocation, limited frames/connections, token exposure and transcript minimization.
  https://cheatsheetseries.owasp.org/cheatsheets/WebSocket_Security_Cheat_Sheet.html
- Linux unix(7): pathname socket permissions and SO_PEERCRED identity from the kernel.
  https://man7.org/linux/man-pages/man7/unix.7.html
- Linux pty(7): interactive pseudo-terminal semantics. The installed systemd parser validates declared unit syntax separately from runtime evidence.
  https://man7.org/linux/man-pages/man7/pty.7.html
- Linux kernel no_new_privs documentation: execve cannot grant setuid/setgid/file capabilities beyond the parent; this is not a guarantee against non-exec privilege paths or kernel flaws.
  https://docs.kernel.org/userspace-api/no_new_privs.html
