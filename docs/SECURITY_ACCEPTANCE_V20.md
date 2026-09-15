# V20 security acceptance matrix

The owner requested validation of existing functionality and network boundaries. Test evidence uses temporary databases, loopback TLS, test credentials and disposable process fixtures only. No production traffic was generated.

| Boundary | What was exercised | Remaining limit |
|---|---|---|
| Login/setup browser origin | Foreign/null/path/query/fragment/default-port/header-ambiguity/Referer/Fetch-Metadata cases, real frontend JSON request contract | No browser exploit or physical-device rendering acceptance |
| Public vs browser API | Missing session and agent bearer cannot enter protected administrative API | Not an exhaustive proof over all dependencies or future routes |
| Viewer vs owner | Viewer reads journal without sensitive capabilities; unsafe request and unknown non-owner role denied | Viewer is not a sandbox for all already-visible inventory metadata |
| Revocation during wait | Body-gated report, role downgrade, logout during SSH settings write; revalidation before effect | No claim that already executed commands are undone |
| Rotating credentials | Superseded/expired/revoked/missing candidate denied; concurrent identical promotion and newer overlap preserved | Real fleet/VM recovery after power loss not tested |
| Revoking credentials | First/second-agent and target/aggregate/event faults; full eligible transaction rolls back | Live channels close on periodic existing revalidation, not instantaneous packet recall |
| Long-lived updates | Eight real HTTPS SSE streams, ninth refused, logout closes and cleans permits; initial write error cleanup | Reverse-proxy timeout and external resource-exhaustion tests not run |
| Expensive authentication | Shared cap, 503 Retry-After, exactly-once release, no automatic frontend retry | Not a DDoS shield; other resources remain independently bounded or require capacity work |
| Local/remote console | Existing WSS/SSH/channel-ticket/origin/role/expiry cases; actual PTY and different-UID tests rerun | No new privilege, no real target admin shell, native systemd cgroup/namespace/boot still required |
| Browser data caching | no-store on browser API, stricter CSP and limited feature policy | Not protection against XSS already executing with owner authority |
| Network provenance | Actual RemoteAddr governs administrative allowlist; forwarded headers ignored; duplicate bearer identity rejected | Deploy behind proxies only after testing real Host/scheme routing |
| Durable state | Initial setup publication and credential revoke transactional; catalogue/lookup errors explicit | Filesystem and DB publication not globally atomic; complete restore remains open |
| Existing behavior | Full Go/Node regression suites; signed worker replacement and bad-candidate recovery on real binaries | Source tests do not replace native OS service or long soak acceptance |

## Mandatory host acceptance

Run the included verification script in an allowed browser environment. Record command exit codes, exact source/build hashes and skipped gates. Do not weaken element locators, +1px overflow tolerance, revocation conditions, TLS trust or rate limits to make tests pass. Preserve both the baseline host 45/45 report and the distinct failing GitHub CI 42-pass artifact as separate evidence.

Keep administrative access restricted to the owner's actual trusted deployment. Do not publish one-time installers or put production credentials into this package, tests or commit history. Keep console local opt-in and independent SSH/provider access. A new limited wallboard credential, MFA, dependency-advisory scanning and complete restore are future work, not silently delivered features.
