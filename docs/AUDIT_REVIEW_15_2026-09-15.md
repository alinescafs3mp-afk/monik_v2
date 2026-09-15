# Monik Audit 15: single-file managed installation and boundary review

Date: 2026-09-15. Baseline commit `ae6c5822843ec74fd4a0838913f1291849691d00`; exact baseline Git tree `ad4cff70f1264e2ce578e947b1407f9886f031f5`. No repository writes, production probes, enrollment, remote shell or native installation were performed by this audit.

## Decision and scope

Implemented a complete source path for an owner-prepared self-contained Linux installer: build-time packaging, authenticated personalization/download, UI, privileged installation wrapper using the existing managed installer, and evidence-based readiness. Preserved worker/supervisor separation and existing installed identities. Reviewed adjacent profile parsing, native installation preflight, JSON authority boundaries and the failed baseline browser fixture.

This is NOT a blanket release certification. The available compiler is Go 1.23.2; production requires Go 1.27.0. Fetching the required toolchain failed, and production dependencies are not present. Exact unchanged-source standalone tests were run for five standard-library-only packages/fixtures in a separate module; the production `go.mod` and lockfiles are unchanged. Full production type checking, server integration, Vue/Vite build, browser execution and systemd boot are outstanding gates for the local integrator.

## A15-01: complex installation was exposed to the operator

Status: source implementation; standalone wrapper and format tests PASS; full integration/native acceptance NOT RUN.

A new `monik-installer` ELF plus worker and supervisor payloads produces one executable file. `monik-installer-pack` creates a credential-free reusable template. The controller streams a copy with its public trust and a scoped single-machine code. The initial owner-authenticated HTTPS download is the trust anchor; checksums detect corruption but are NOT a detached signature against a malicious distributor. Future worker upgrades still use the existing signed mechanism.

Bounds: 64 MiB per executable; 64 KiB manifest; exact part sizes/digests/ELF architecture; static binaries only; no path names from the manifest; one selected HTTPS origin; no arbitrary shell command field; no self-download/execute from a URL. Generic templates cannot already contain a profile. Template directories are opened beneath controller data, symlinks/unsafe modes rejected, and build identity must match the server.

The one-use code expires in an hour. Creation and credential-free audit entry share a DB transaction. The endpoint requires owner session, CSRF, recent auth and direct HTTPS, refuses restore mode, bounds outstanding codes (32 per creator), limits download frequency and serializes preparation. Browser code never persists the profile/secret and never silently repeats an uncertain download. Aborted downloads leave an unused expiring code, not a fleet command.

## A15-02: ready had to mean actual managed operation

Status: wrapper logic, TLS exchange and filesystem helpers PASS in isolation; real systemd/server route integration NOT RUN.

The wrapper invokes existing setup then native service installation from fixed protected staging paths. It uses root only for installation; the existing dedicated non-root service identity remains. No nohup/disown fallback. Unsupported OS/service managers fail explicitly.

Ready requires enabled service, active service, a responsive supervisor with a worker PID, hashes of the kernel-referenced worker/supervisor executable handles matching installed files, and two distinct fresh committed report sequences for this agent/session/current applied config. Report readback uses the installed agent credential and can read only that agent. A loaded `managed` flag alone is insufficient. Readiness has a 90-second bound; the service may remain installed/retrying after a readiness timeout but this is NOT reported as ready.

An already active managed installation is checked without replacing its binaries, URL or identity. Expired original enrollment permission does not prevent this read-only installed-state check. Foreign/pending identity or different stopped installed binaries require explicit recovery, not an automatic destructive reinstall. Failures after enrollment keep protected state. Independent service-host self-update and full rollback of every native installation step are not implemented here.

## A15-03: source preflight occurred after mutation

Status: code corrected; new full-project regression supplied, NOT RUN here.

`InstallLinux` previously could stop a working service and replace the supervisor before discovering that the worker source was missing/unreadable. Both source byte sequences are now bounded/read before mutation, validated as matching Linux ELF on the real native path, and retained through installation. A missing second source cannot partially publish the first. The test asserts the previous installed file and state directory remain unchanged. This does not promise transactionality for subsequent disk failures across every systemd/filesystem operation.

## A15-04: ambiguous incoming JSON authority

Status: reproduced on baseline exact `internal/server/json.go`; fixed-source tests PASS.

The previous request reader accepted duplicate object members, including escaped duplicate parameter names. Baseline regressions show two failing subcases. It now uses existing V13 `jsonutil` validation before typed decoding; second JSON documents, null and size limits stay rejected. The new installer request also rejects unknown fields. No schema/version downgrade or crypto implementation was added.

Profile JSON previously fell back to YAML on JSON errors; YAML loading did not enforce one mapping/document or a strict field set. Profiles now use bounded reads, unambiguous JSON without fallback, or exactly one typed YAML mapping. Generated `schema_version` is preserved. Enrollment and announcement responses use the existing unambiguous JSON decoder. The new profile/enrollment tests need the full Go toolchain/dependencies and have not been executed here.

## A15-05: baseline browser failure was not a missing Services control

Status: baseline CI failure confirmed; bounded fixture-retry helper tests PASS; new browser run NOT RUN.

The downloaded CI artifact for run 34954581114 contains a locator timeout after 30 successful scenarios. Its `fixture.log` shows SQLite BUSY/LOCKED errors followed by a fatal synthetic telemetry-feed exit. The test then waited for a UI control after its server was gone. The fixture now retries the SAME immutable report/event only on SQLite BUSY/LOCKED, with a deadline. Non-transient errors and persistent locks still fail. Exact control locators remain unchanged; a machine-API assertion now reports the underlying failure earlier. Existing admission tests explicitly open the newly collapsed advanced section. No browser policy bypass was attempted.

This change is not evidence that the entire database contention problem is solved in production. The full browser run and load/retention tests remain required.

## A15-06: protect new privileged reads and partial output

Status: exact-source tests PASS; executable format smoke PASS.

The wrapper opens protected path components with openat/O_NOFOLLOW; final nonblocking open avoids FIFO-open hangs before regular-file validation. Credentials require private regular single-link files and bounded size. Installed executable hashing uses the opened file handle. System commands use fixed trusted tool locations, bounded contexts and sanitized environment. The package has an installer lock independent of the existing native installer lock. Payloads are staged privately, no secrets are passed in argv, and normal errors clean staging. Pack-time errors unwind through a returning function rather than os.Exit so temporary files are cleaned.

A real combined ELF using the newly built wrapper and exact BASELINE Audit14 payloads was run with `--inspect` only. It started successfully, did not disclose the synthetic enrollment code, and rejected a corrupted payload. Those binaries are test fixtures, not current V15 agent builds and not distributed in this package.

## Verified evidence and remaining validation

- Independent Go 1.23.2 harness: 29 top-level tests/fuzz seed targets, 50 test/seed results, 5 tested packages, race detector PASS. Production sources were byte-for-byte copied without fake dependency implementations; see `standalone-sources.json`.
- Same subset: vet PASS, 10 shuffled repeats PASS.
- Bundle fuzz: 90,575 executions in a requested 15-second run, no found failure.
- Frontend: 123 Node tests PASS, including existing suite and new download safety tests. Some tests inspect source contracts, not rendered components.
- Exact API TypeScript file type checking PASS. Whole Vue/Vite build NOT RUN because locked frontend deps are absent.
- New installer/packer static amd64 builds and installer arm64 cross-build PASS using local toolchain. This is NOT a rebuilt application distribution.
- Complete source Go syntax parse PASS; syntax parsing is not type checking.
- Full production Go suite/agent build, server/storage/native installer integration tests, browser acceptance, actual clean-systemd install/close-terminal/boot, Windows services and physical TV: NOT RUN.

The source packet is intended for mandatory full integration validation by Grok before deployment. Do not copy previous audits' test counts into V15 acceptance. Do not call the one-file installer production-ready until the supported toolchain build and native pilot pass.
