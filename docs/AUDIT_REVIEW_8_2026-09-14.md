# Monik Audit 8: dense wallboard, service names, SSH console, native agent hardening

Date: 2026-09-14. Application baseline: `4e0ae20aeefb95663a5a1618facc42f80b36dee8`. Integration baseline: `3873c9ce567aaef9385c98a897d9d542f9ada52f`, tree `e13136929ff7d2562ab668a2ab409dee8f9f53c5`. The latter adds only the pinned terminal-dependency CI workflow. Preserve it and all later owner changes.

**This is corrected source, not a production deployment or a complete-release certificate.** No request to the owner's live controller or remote server was made. Application publication status is recorded in the outer handoff, not inferred from the existence of a source archive. Historical audit documents remain historical.

## Delivered owner requirements

1. TV default font changes from 14px to 10px, approximately 28.6% smaller. Density options remain 10/12/14px. The TV-specific preference key resets this default once; existing ordinary/mobile display choices are preserved. This is a responsive layout, not a browser zoom workaround or a scaled screenshot.
2. The TV navigation is a bounded uniform grid; links share padding, alignment and height. Header groups, credentials and controls wrap within available width. The board measures actual visible row height, uses bounded pagination and does not overlay controls. Physical Yandex TV rendering has NOT been accepted here.
3. Confirmed/current problematic host metrics or failing **selected** service results give the entire machine row/card a red border and sort it first. Stale/unreachable contact is a separate priority tier. Unselected-service failures retain their existing indicator but do not create a red selected-scope priority. Paused/not-monitored results do not become failures. Acknowledging an incident does not repair a metric/service. Sorting is deterministic by priority/name/ID, never by every fluctuating CPU sample. A newly problematic TV machine returns the pager to the first page.
4. Service names can be edited from grouped Services and machine details. A server-side compare-and-swap preserves concurrent edits. Name changes preserve the service ID, URL, request, secrets, history and scheduling. Rediscovery retains the owner name. Validation rejects empty/control-containing names and limits UTF-8 bytes. A baseline integration test failed with `unknown_action` before this implementation.
5. A per-machine Overview/TV button opens a real interactive **SSH** console. This is an explicitly requested scope addition, implemented independently of the monitoring agent. It requires a protected controller-side mapping, a reachable SSH server, an independently verified host-key fingerprint and SSH credentials supplied for that connection. It does not install SSH, tunnel through agents or reuse the Monik owner password as an SSH credential.

## SSH console boundary and proof

The controller connects only to exact configured IP/port/account/fingerprint entries in `console-targets.json`, not arbitrary destinations submitted by a browser or remote agent. Revoked/archived machines and restore mode are denied. The file is bounded, strict JSON, regular and not writable by group/others. Loopback is possible only through an explicit local administrator configuration; metadata/link-local/multicast destinations are denied. DNS targets and SSH certificate authorities are not implemented in this slice.

The browser must have an owner session, CSRF-protected ticket request, recent authentication and a same-origin WSS connection over actual TLS. A one-use 30-second ticket binds session, machine and target configuration. Credentials are sent in the first authenticated WSS message, never in URLs. Invalid SSH host keys are rejected before user authentication. Password and encrypted/private-key authentication are supported; keyboard-interactive/MFA is not.

Connection limits: four total, one per machine, 12 starts/minute/owner, 32 pending tickets, bounded frames/input/output, 16 MiB total output, 60 minutes total and 10 minutes without input/resize. Exceeding the output limit aborts the transport. Session revocation and changed target/agent authority are checked every five seconds. The connection is not automatically resumed and keystrokes are not retried. Output is untrusted xterm data; OSC 52 clipboard writes and terminal-link activation are disabled. Multiline paste asks for confirmation. Terminal modules are lazy loaded and included locally, not from a CDN.

Audit records connection open/close/error metadata, **not** commands, terminal output, passwords or private keys. Credentials are not persistently stored by Monik, but necessarily exist in process memory during authentication; this is not a claim of secure erasure from all runtimes. Trusted same-origin UI and controller administration remain security dependencies. Closing a terminal cannot guarantee all previously started remote processes have exited. A saved owner login on a shared TV is not a viewer role.

Eight Go console tests pass, including real SSH transport negotiation, PTY request and resize messages, bidirectional data, wrong-key-before-auth rejection, one-use identity-bound tickets, CSRF/origin/owner/restore checks, encrypted Ed25519 key authentication, output budget and session revocation. The SSH fixture implements an echoing session: this is **not** native Bash/PowerShell acceptance. Browser interactive xterm acceptance is still blocked in this environment.

## Linux installation and worker lifecycle

The native installer now provisions/validates the dedicated non-root account, establishes private state ownership, stores the immutable supervisor under the root-owned prefix and places its active worker in a private service-owned slot for signed worker replacement. It persists managed configuration before service startup. It reloads enrollment after stopping an existing service so a concurrent URL/credential change is not overwritten by an old preflight copy.

Elevated installation uses a global lock, fixed trusted native tool paths, deadlines, protected path ancestors and descriptor-relative state publication. It rejects dangerous base paths, linked state and hard links rather than recursively changing arbitrary filesystem ownership. It does not change router/firewall rules. File ownership changes are confined to the explicitly selected agent state. Existing enrollment is retained. CLI printed commands are quoted; `doctor`/`controller show` do not enter a blocking approval loop or mutate normal state.

A newly reproduced supervisor bug let `Run` return before stopping its worker, allowing the parent to exit and leave a child behind. The stop path now waits for synchronized worker teardown before returning. A new baseline-failing regression proves this ordering. The actual compiled supervisor additionally passed worker-kill/restart and clean parent-stop tests.

**Remaining installation boundaries:** native systemd provisioning/boot was not run here; Windows restricted account/ACL/SCM implementation remains incomplete despite cross-compilation. Linux installation is not a complete transaction with automatic rollback at every failure; a failed reinstall can leave a stopped service requiring a deliberate rerun/recovery. Independent service-host self-update/recovery is still unavailable. Check the dedicated account and production path policy on the first real remote host, retain an independent SSH recovery path, and do not infer full fleet readiness from process tests.

## Further audit correction

`LatestHost` and `HostAt` previously ignored corrupt JSON/null rows and could return an apparently successful zero-valued host snapshot. They now reject malformed/null payloads and malformed stored timestamps with an actionable error, without echoing the payload. Both corrupt cases were observed failing on baseline before correction. No history or live host identity is silently reset to fix malformed data.

## Executed evidence for this source

| Check | Result and exact scope |
|---|---|
| Go race suite | PASS: 211 top-level tests, 276 test/subtest pass events, 19 tested packages. Two opt-in native tests are skipped in this ordinary run and executed separately below. |
| Go vet; Go module verification | PASS |
| Frontend Node tests | PASS: 78. Includes pure helper and source-contract tests, not 78 browser journeys. |
| TypeScript | PASS: `tsc --noEmit`, not a separate `vue-tsc` acceptance. |
| Production Vue/Vite build | PASS with locked dependencies, xterm 6.0.0 and addon-fit 0.11.0. |
| Linux amd64 builds | PASS: server, worker, service-host and release utility. Unsigned audit binaries, not a distributed production release. |
| Windows amd64 builds | PASS: the same four cross-builds; no native Windows execution. |
| Separately compiled native worker | PASS: actual CPU/RAM collection as UID 65534, clean termination, new-session restart with identical agent ID/URL, HTTP 503 ingestion/control interruption, persistent spool and recovery drain. |
| Separately compiled native service-host + worker | PASS: non-root supervision, killed worker respawns, clean supervisor shutdown/restart, same identity and spool recovery. Does not run systemd/SCM. |
| SSH integration subset | PASS: eight cases described above, including encrypted key login and explicit resize acknowledgement. |
| Regression before fixes | CONFIRMED: missing service.rename, corrupt/null host snapshots and supervisor premature return. |
| New local browser attempt | BLOCKED by `ERR_BLOCKED_BY_ADMINISTRATOR` before loopback login; zero successful browser checks in this attempt. No workaround attempted. |
| Updated browser script | Syntax checked only. New CI native/browser steps NOT RUN remotely at package time. |
| Physical TV/Yandex/mobile, OS boot, power loss, full fleet, 24h soak | NOT RUN. |
| Owner deployment | NOT ACCESSED OR MODIFIED. |

A time-limited full-suite command and the first supervisor smoke attempt were interrupted by tool timeouts; those incomplete logs are preserved separately and are not counted as passes. Subsequent complete final runs are named `*-final` and the before/after supervisor evidence is retained. Go 1.27.0 and Node 22.16.0 were used with pinned dependencies. The source/patch reproduction and actual hashes are recorded by package validation.

## Remaining release work, not claimed fixed

Independent native supervisor replacement/recovery; Windows restricted identities and correlated local IPC; immutable release publication and cohort/pause/resume management; full encrypted/controller backup and stale-job restore reconciliation; long-term metric aggregates/capacity; robust offline secret lifecycle; viewer-only wallboard authorization; native/browser acceptance. Existing unsupported actions stay guarded. The separate SSH console must not be used to conceal incomplete agent lifecycle operations.

## Primary references checked during this pass

- Go SSH API: https://pkg.go.dev/golang.org/x/crypto/ssh (host-key verification, PTY and immutable configuration).
- xterm security guide: https://xtermjs.org/docs/guides/security/ (untrusted terminal data, explicit WebSocket authentication/origin boundaries).
- Go traversal-resistant APIs: https://go.dev/blog/osroot (descriptor-relative path confinement, not a complete filesystem privilege model).

These references support implementation constraints, not certification of Monik or of the owner's deployment.
