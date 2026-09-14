# Monik V12: stabilization and deployment-readiness audit

Date: 2026-09-15. Exact reviewed commit: `c2229685f76551bcbdb0bb89e1fe1adc16e28878`.
Baseline tree: `5b091979aa6ed5253397dfdfcd2dc0c8c0758fc5`.

This is a corrective source package, not a signed binary release and not evidence of production deployment. GitHub and the owner's running installation were not changed. Existing monitoring/UI capabilities are preserved. There is no new remote-execution mechanism. The optional SSH gateway remains separate from the agent and disabled without its protected target configuration.

## Decision

**Suitable for a controlled Linux pilot AFTER the local integration, browser and systemd/reboot gates below pass. Not certified for unattended fleet deployment or complete disaster recovery.** The real signed worker-upgrade scenario now passes; independent supervisor self-replacement, Windows service provisioning/boot, complete protected restore and long-retention acceptance remain open.

The user requested verification of implemented behavior rather than another feature layer. This pass prioritizes causal regressions, corruption rejection, shutdown ownership and a real signed update/rollback process test. A green unit suite alone did not establish these properties.

## 1. Starting evidence and the previously failing CI

GitHub Actions run `34897729355`, job `104155920662`, on the exact baseline failed at `TestAudit4TrialDoesNotBlockControlAndIsNotRepeated`: `trial blocked dispatch`. The test required return within 100 ms although dispatch durably synchronizes a journal. There was no data-race diagnostic in that failure. Later build, native-process and browser steps were skipped. The baseline's locally recorded prior PASS statements are historical, not evidence this CI run passed.

The baseline suite passed locally. The rewritten trial test proves causal independence: hold the HTTP response open, verify dispatch returns, verify another diagnostic job can execute, verify duplicate delivery does not issue a second request, and only then release the response. A generous watchdog catches deadlock, not machine speed. No assertion was removed merely to turn the build green. CI now preserves JSON Go results on failure and runs the new real-binary signed-update scenario after building.

## 2. Agent findings and corrections

### Lost completion after an acceptance acknowledgement

An HTTP request could contain only an accepted/pending receipt, while its trial completed during that request. The response's acknowledgement then removed the current completed receipt by job ID, before completion was ever transmitted. The worker now compares the immutable sent receipt with the current record, and removes only the exactly acknowledged terminal record. Pending/accepted acknowledgements cannot erase a later outcome. A local HTTPS regression reproduces the original loss and tests the subsequent final-ACK path.

### Update restart loop discovered using actual binaries

The requesting worker is stopped by its supervisor before it can finish `handleJob` and write its result. Its successor initially sees an update journal still in probation. Without a reserved pending receipt, its very first poll can receive the same update job and start replacement again. The first real V12 signed-update run timed out while repeatedly producing new worker sessions. Unit tests had missed this ordering.

On startup the successor now restores a pending receipt from the durable lifecycle intent BEFORE its first poll. It never repeats that action while waiting for the existing supervisor transaction. Completion still requires the correct supervisor journal stage, expected digest and a different worker session. An incomplete journal cannot become success. The corrected native test performs a real signed upgrade and then activates a deliberately failing signed candidate, verifies restoration of previous-good bytes, a new live session, and a blocked rollout rather than a successful update.

### Configuration and concurrent transport

A configuration revision is immutable. A second different body carrying the same revision and a valid self-hash no longer replaces the accepted body. Publication of its body/hash/revision is serialized with runtime readers. TLS trust staging AND controller rebinding now replace the entire HTTP client/transport snapshot; they do not modify a client still used by an in-flight secret or report request. Timeout and redirect restrictions remain intact.

### Durable journals and outgoing buffer

Receipt journals must be bounded regular files containing an object of matching job IDs and known statuses. Null, invalid shape, oversized files and read errors fail startup without overwriting the evidence. Lifecycle intents likewise have bounded reads and validated kinds/required identity. Interrupted pending trials become an unknown interrupted outcome, not automatic repeat.

A syntactically valid `null`, empty object or incomplete report in the spool used to masquerade as a valid envelope and obstruct delivery of later good entries. Records now require report identity, positive sequence and observation time. Invalid files remain diagnosable; valid following records are still returned. This does not implement a full cross-restart loss-interval ledger.

### Worker cancellation and single-process ownership

`Run` cancels and joins owned trial, check and discovery tasks before returning, so a detached task cannot keep emitting requests or writing its journal after shutdown has been reported complete. Real network trials are covered; arbitrary hanging OS sensor calls are not made magically cancellable.

Crash-released local locks prevent two running workers from using the SAME state directory/identity, two supervisors from replacing the same socket, and two controller Run instances from serving the same data directory. The Unix lock file keeps its inode; it must not be deleted to force access. Locks are local, not fencing for copied databases on different machines. Old binaries do not honor the new locks, so deliberate stop/start is still required during transition.

## 3. Supervisor and controller lifecycle

A supervisor could accept a late control request and restart a worker after Run had stopped. It now closes ingress, rejects new mutations, cancels control handlers/reaper work, waits for owned tasks, and finally stops the child. The controller similarly binds before starting background workers, cancels and joins schedulers, drains HTTP before the store may close, and treats an ordinary requested server shutdown as a normal result.

The SSH gateway now tracks pending as well as fully authenticated WebSocket sessions. Shutdown revokes unused tickets, prevents admission of new sessions and closes/waits for existing connections. This closes an Add/Wait and pending-authentication lifetime gap; it does not expand SSH access or store credentials.

Recovery must prove previous-good bytes. A rollback with no recorded old digest is rejected. Restoration reads/verifies/copies one byte snapshot, eliminating a hash-check versus second-read mismatch. Activation failure uses one bounded recovery path. If restoring bytes, saving the recovery journal or starting the old process fails, the response cannot claim verified rollback. A test candidate deliberately makes its own temporary recovery journal unwritable before exiting; the result is explicit recovery failure, not a green rollback.

## 4. Controller identity and storage

Missing controller identity on an enrolled database is now an error rather than creation of an unrelated identity. A missing/empty database beside existing private keys/TLS material is not treated as a clean first setup. An established controller cannot silently generate a replacement CA after loss of its TLS directory. CA and private key must match, the CA must be valid, and the stored leaf must be signed by that CA.

These are conservative protections against partial copies, corruption and wrong restore directories. They are NOT a full backup/restore implementation. Restore the original consistent state, do not delete keys or run initial setup to evade the error. Deliberate CA rotation still needs its proper overlap workflow.

SQLite filenames are encoded as file paths, not concatenated unescaped into a URI. Literal `?` and `#` in a path can no longer select URI options or a different database accidentally. A close/reopen regression proves persistence at the requested filename.

Frozen rollout membership must join to the correct target and job with operation/agent ownership. Missing jobs previously disappeared from an INNER JOIN, potentially making an incomplete plan appear smaller. Read/claim now checks the joined set against frozen members and targets and refuses a corrupted plan instead of silently advancing it.

## 5. Web interface and assets

Missing JavaScript/CSS assets now return 404 with `no-store`, not the SPA index with HTTP 200. The HTML entry point requests cache revalidation; content-hashed assets may remain immutable. Valid application deep links still work. This addresses stale tabs after deployment without silently forcing a reload.

Vue Router navigation errors have a visible global alert, a manual reload button with a warning about unsaved drafts, and a dismiss action. Raw failing URLs are not echoed. No automatic reload, command repeat or reconnect is introduced. A fresh successful navigation clears the notice. Existing TV density, machine/service pinning, independent monitoring selection, human names, problem priority, history axes and operation read state remain intact.

A new browser case checks a missing asset and intentionally rejected lazy-loaded Settings module. It verifies visible feedback and no mutation/reload. This scenario is delivered but NOT executed successfully here: the permitted browser attempt was blocked before login by `ERR_BLOCKED_BY_ADMINISTRATOR`. No alternate path was used to bypass that policy. Source/helper tests are not visual acceptance.

## 6. Evidence ledger

See `evidence/validation-summary.json` in the delivery package and `docs/audit/validation-review12-2026-09-15.json` in source. Logs retain their actual sandbox timestamps (September 14); document date follows the session date (September 15). No clock values were rewritten.

The first failing native update log is retained separately from the successful corrected run. There are 23 distinct top-level names in negative-test logs, including overlapping manifestations and tests against intermediate audit code; this is NOT a claim of 23 independent security vulnerabilities. The complete final race suite, targeted repeats, frontend/type/build checks and real process checks are separately identified.

The fuzz target mutates bounded custom-check definitions without network I/O. Its completed 15-second run executed 279,067 cases without a found panic or violated acceptance invariant. An earlier invocation was interrupted by the tool execution timeout and is not counted as a completed fuzz run. Seed coverage, mutation coverage and native tests are distinct evidence categories.

### Real-binary update scope

Linux amd64 worker + supervisor built from the corrected source run under UID 65534 when the harness starts as root (otherwise its unprivileged UID). A local HTTPS controller uses real SQLite and independently enrolled generated signing trust. A functioning candidate is the same ELF with an inert trailing marker, producing a genuinely different signed digest and process. This proves replacement/session/receipt/observation mechanics, not every possible cross-version schema migration. A second signed shell candidate exits immediately; the supervisor restores the verified previous worker. No OS service is installed and no public host is contacted. One agent is exercised, not a production multi-host fleet.

## 7. Unchanged release boundaries

1. Native systemd provisioning/reboot and Windows SCM/account/ACL/boot tests remain local deployment gates. Cross-compilation is not acceptance.
2. Independently recoverable self-update of the service-host is NOT implemented and stays guarded. Correct worker rollback is not supervisor self-recovery.
3. Full protected-controller backup/restore with restored-job reconciliation and cross-host single-writer fencing remains incomplete. Local locks do not solve split brain.
4. Thirty/180-day aggregates, measured large-fleet capacity, long-running retention/exports and a 24-hour soak remain unproven/incomplete. Recent raw graphs are not that proof.
5. Generic selected-operation retry stays guarded. Do not resurrect expired or uncertain disruptive actions by reading notifications or editing the database.
6. Windows file-control correlation, all OS sensor deadlines, complete key/root rotation and offline secret-cache lifecycle still need dedicated native review. This package does not claim a vulnerability-database scan or elimination of every possible flaw.
7. Physical TV/Yandex, mobile and full browser UI acceptance must be run outside the blocked audit browser environment. Existing screenshots from earlier commits are not screenshots of this source.

## 8. Integration and operational cautions

Use the one patch for `c222968...` in a clean worktree, preserve concurrent main changes, and verify the package. Rebuild UI before embedding the server. Use the matched new worker and supervisor for the first pilot. Retain the real selected controller URL and enrolled IDs/credentials/trust. Update server/UI before new agents. Do not run setup/identity reset on an enrolled state directory.

No downgrade of a server with active v11-format rollout jobs. No concurrent old and new controller writers. Pause/settle lifecycle operations before a VM move. Copy the complete stopped protected state and all externally referenced configuration/trust files through an owner-approved backup procedure; the existing DB-only helper is insufficient. Keep independent SSH/hypervisor access until systemd/reboot/update checks succeed on the target machine.

A corrupted-journal startup error is an actionable stop, not a request to erase the journal. Preserve files and verify the operation against the controller and previous-good backup. Never delete a live lock file; stop the actual owning process. No lock deletion is needed after a real process exit.

## Primary technical references

- Go net/http: https://pkg.go.dev/net/http (reuse clients/transports safely; publish replacements rather than racing field mutations).
- SQLite URI syntax: https://www.sqlite.org/uri.html (reserved query/fragment characters are not literal filename characters without encoding).

These documents explain constraints, not certify Monik. Defaults, findings, process results and remaining limits come from reviewed source and the attached tests.
