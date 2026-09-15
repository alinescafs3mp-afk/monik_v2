# Monik V20: existing-function and network-security audit

Date: 2026-09-15. Reviewed commit: `0385030878ec58e0f7a8bf1b95ab1f38fd1cde06`. Baseline tree: `b948195118d1b08bf765106ee55d78b52555a7ce`.
No production access, production credential use, deployment or GitHub writes. This is a cumulative corrected source tree plus one patch to the reviewed commit, not a chain of earlier patches. Scope: authority boundaries, registration/credential lifecycle, durable operation outcomes, browser mutation policy and bounded event/authentication work. Existing agent terminal privileges were NOT expanded.

## Findings reproduced on the unchanged baseline

`evidence/regressions-clean-base.log` records the original ten failing test groups. `evidence/revocation-before.log` records an additional security-relevant defect found during the second review. These are eleven regression groups, not eleven independently assigned CVEs. Preconditions matter.

| Finding | Preconditions and observed old behavior | V20 correction |
|---|---|---|
| Foreign-origin login | The handler accepted a request carrying a foreign Origin, including text/plain containing valid JSON and valid fixture credentials. This did NOT bypass password verification. | Same-origin browser mutation policy, JSON-only login/setup, Fetch Metadata and Referer checks. No browser exploit is claimed executed here. |
| Viewer registration capability disclosure | An authenticated viewer could read a live one-time enrollment code in operation-list/detail evidence. This is not an anonymous/public leak. | Non-owner views redact params/evidence for sensitive and unknown actions without modifying stored evidence or the owner's result. |
| Stale report credential | The report was authenticated before reading its body; rotation while the body was held did not prevent the old key from submitting telemetry and receiving a response. | Reauthenticate the same transport credential after body parsing and serialization, before accepting the report. |
| False empty API success | Failed operation lookup/catalogue reads were presented as absent operations or empty secrets/backups. | Distinguish not-found from storage failure. Lookup ambiguity must not authorize a blind replay. |
| Partial first-owner setup | A settings write failure still allowed creation of the first owner. | Atomic initial settings, owner and audit publication; update in-memory configuration only after commit. TLS files are separately prepared, not a fictitious filesystem/database transaction. |
| Cached owner role | A request held while reading its body could dispatch an operation after the role had changed to viewer. | Refresh session principal, token, CSRF, role, expiry and recent-auth after waits. Require exact owner authority. |
| Console configuration after logout | An already authenticated request could save console target configuration after its browser session was deleted while its body was held. | Revalidate after parsing/lock wait and after audit intent; recheck target agent eligibility. |
| Old owner login issuing codes | Manual enrollment-code creation did not require recent authentication, unlike the one-file installer path. | Mark enrollment.create as recent-auth-required; use the existing safe client reauthentication flow. |
| Stale credential promotion | A cached candidate could replace current credentials after its pending rotation was superseded, expired, revoked or missing. | Transactional check of current and pending credential, exclusive expiry and agent revocation, compare-and-swap update and exact pending-row removal. |
| Inclusive overlap deadline | Pending credentials were still accepted exactly at expiry. | Exclusive expiry with parsed timestamps; expired cleanup cannot erase a concurrently replaced pending row. |
| False successful credential revocation | An injected write failure left the credential active while the revoke operation reported success. | Revoke the eligible frozen target set, clear its pending rotations and publish target/aggregate/attention/event state in one transaction. Failure rolls back the whole eligible batch. No silent partial success. |

### Additional boundary hardening

* Real browser mutations require an exact origin matching scheme, hostname and effective port; default ports are normalized, paths/query/fragments/user-info and duplicate Origin headers are refused. Forwarded headers are not a source of trust. Native clients without browser metadata still require credentials/CSRF where applicable and JSON media type for login/setup.
* Unsafe administrative methods require owner, rather than merely rejecting the known viewer role. Role changes during a request cannot silently grant extra authority. Long-running body, lock and audit windows are revalidated for protected writes and installer issuance. These checks are not a claim of globally atomic authorization across all filesystem and network operations.
* Password hashing/verification has a shared, non-queuing limit of two active jobs across login, reauthentication, password change and HTTP setup. Excess requests receive 503/Retry-After; existing source/user rate limits remain. This bounds this specific expensive work, not all network resource consumption and not a volumetric DDoS defense.
* SSE streams are limited to eight per session and 128 per controller, with exact-once permit cleanup. Initial and subsequent writes/flushes are bounded; I/O failures release the slot. Session/user/role are rechecked on each heartbeat. A real HTTPS test opens eight streams, observes refusal of the ninth, revokes the session and observes closure/released slots.
* Cookie-authenticated API responses are no-store. CSP adds base-uri none, object-src none and form-action self. Permissions-Policy disables camera, microphone, geolocation, payment and USB; fullscreen and clipboard are not newly disabled.
* Duplicate agent transport identity headers and duplicate WebSocket Origin are rejected before using ambiguous identities. Agent bearer credentials do not authorize the administrative API. Forwarded-IP spoofing does not bypass administrative CIDR checks.
* Identical simultaneous credential promotion is idempotent and preserves a subsequently staged rotation. Credential-revocation faults at the second agent, result, aggregate and event steps all roll back earlier changes. A committed revoke becomes visible to new requests immediately; existing consoles close at their next existing authorization check, not by retroactively undoing commands.

## Existing functionality and compatibility

Telemetry, custom HTTP checks, selective monitoring, names/pinning, query history, human read/unread state, immutable signed releases, held canary/batch rollout, isolated opt-in agent terminal, direct pinned SSH and shared TV profile are retained. No protocol schema, dependency lockfile, agent binary implementation or service-host policy change is required for V20. No new inbound agent port, elevated remote shell, endpoint change or automatic console opt-in was introduced.

Previously created sessions and agent credentials are retained. Reload browser tabs with the new server/UI. A manual enrollment request may now prompt for the owner's password through the existing recent-auth dialog. Native callers of login/setup must send application/json. A TLS-terminating proxy must not be treated as trusted merely because it supplies Forwarded headers; V20 has no implicit trusted-proxy mode. Validate the real Host/scheme/TLS topology rather than disabling the checks.

## TV and baseline CI investigation

The baseline main commit's separate Verify workflow passed, but GitHub CI run 35010468242 failed its browser step after 42 successful scenarios. The source's host acceptance report also records a separate local 45/45 run; these are different executions and neither overwrites the other.

The downloaded CI result contains an empty AssertionError description. Comparing its last completed markers with the exact script narrows the uncompleted segment to the local-mode/390px-TV portion of the shared-profile scenario. The artifact does NOT prove the exact failed assertion or an exact browser root cause.

V20 adds a conservative immediate narrow-screen media-rule fallback for the TV grid while ResizeObserver/Vue update their measured tracks. It stacks identity/services/actions and wraps metrics; it does not hide overflow with clipping. The browser check now waits for the actual narrow layout to settle, preserves the original +1px overflow tolerance, and writes offending element geometry/screenshot on failure. The outer harness records a traceback instead of only str(exc), so an empty AssertionError no longer erases the failure location. No locator or geometric tolerance was weakened.

A normal final-browser attempt is recorded separately. A policy block before login is NOT a passed visual gate. Physical TV/Yandex, remote controls and true cross-device geometry remain host checks.

## Self-review and validation interpretation

One defect was caught in V20 itself: a bare trailing fragment delimiter in an Origin was accepted by the first parser draft. The same negative test now passes; both outputs are retained. An early transaction test referred to the wrong fixture table name; the corrected test targets the real event_log table and verifies rollback rather than suppressing the injected failure.

Authoritative commands, actual test counts, skips and exit codes are in `evidence/validation-summary.json`. The full Go race suite, frontend tests/build, focused repetitions, module integrity, static checks, native process/update recovery and isolated terminal tests must be distinguished. Node tests include actual API execution and source-structure assertions, not a rendered-browser proof. `tsc` is not a separate `vue-tsc` pass. Fuzzing is a bounded input exercise, not an exhaustive proof. Dependencies were obtained from supplied offline caches and unchanged lockfiles. `go mod verify` is integrity validation, not a current advisory/CVE scan.

No independent external pentest, TLS/firewall exposure test on the owner's VM, native service installation/boot, Windows SCM, power-cut exercise, large-fleet load test, physical TV or 24-hour soak is claimed. Full protected restore, independent service-host self-update/recovery and long-history aggregates remain unfinished release gates.

## Primary reference rationale

OWASP CSRF Prevention: https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html

OWASP WebSocket Security: https://cheatsheetseries.owasp.org/cheatsheets/WebSocket_Security_Cheat_Sheet.html

These references informed origin/metadata, revocation and resource-bound checks. They do not certify Monik, its dependencies or the owner's deployment as secure.
