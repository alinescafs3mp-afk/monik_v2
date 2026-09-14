# Monik Audit 10: immutable release publication

Date: 2026-09-14. Reviewed baseline: `ce602d01b3bc335a94b194bf1c3c04e7e46a25ad`. Baseline tree: `9a5c10dc7e9640ae3d442a65f444d3fb4b917b1e`.

**PRE-RELEASE. Source-only corrective pass. The owner's running controller and machines were not accessed, restarted or changed.** This pass implements a specific previously open release-publishing boundary; it does not declare native update recovery, full restore, rollout cohorts or the entire product complete. Earlier audits remain historical, not acceptance of this revision.

## 1. Confirmed baseline defects

Four added regressions were run on the unmodified baseline and failed; their log is `evidence/regressions-before.log`.

1. `TestAudit10ImportPreservesOldArtifact`: importing signed B replaced the bytes at the catalogue path for A. The original path returned `second-worker` rather than `first-worker`. A pending A update therefore depended on mutable shared files.
2. `TestAudit10CatalogRejectsPartialArtifactCommit`: an injected SQLite failure inserting `release_artifacts` still left a catalogue row and operation `completed`. The baseline ignored important persistence errors.
3. `TestAudit10CorruptSigningKeyIsNotReplaced`: initialization silently replaced an existing malformed signing key. The old bytes were destroyed instead of requiring recovery.
4. `TestAudit10NullHighWaterIsRejected`: JSON `null` was accepted as a zero-valued metadata version journal, resetting rollback protection.

The same regressions pass after the changes. The full baseline suite also passed before these additional tests; baseline green tests did not cover the defects.

## 2. Implemented release path

`internal/tufutil/publication.go` stages and verifies a bundle against the independently enrolled root (or an explicit first enrollment), authenticates linked metadata, checks target bytes, and builds a deterministic inventory of sorted relative names, lengths and SHA-256 values. The SHA-256 of that inventory is the immutable release ID. Metadata and target files are copied into a sibling staging directory, checked and synced, then renamed into `tuf/releases/<digest>`. An existing digest directory must match its inventory; it is never silently repaired or overwritten.

Archive extraction is bounded to 512 entries, 512 MiB total expanded bytes, 200 MiB per entry and 2 MiB per metadata JSON outside targets. There must be 1..128 signed targets. Signed target names are validated before local reads, and catalogue ordering is deterministic. Symbolic-link publication directories and links discovered during complete object verification are rejected. User/operator-provided bundles are not automatically executed by the importer.

`internal/storage/release_publication.go` publishes the catalogue, every artifact row, the publication inventory, enrolled root/high-water state, and successful operation/attention/event result in a **single SQLite transaction**. A failed artifact insert or outcome write rolls back all database changes. A repeated identical import reuses verified immutable files and does not duplicate catalogue entries. A revision check prevents a stale trust snapshot from committing over a newer one.

This is deliberately **not** a claim of one atomic filesystem-plus-SQLite transaction. File publication happens first. A failed database commit can leave an unreferenced complete object; it is not visible through the release API. A later explicit retry can verify and adopt the same content. There is no automatic release/orphan garbage collector in this pass. Account for staging/free space, retain objects referenced by jobs, and inspect orphan cleanup only during an operator-controlled maintenance procedure. Windows directory-sync and real power-cut behavior were not validated here.

After the first new-style publication, SQLite `release_trust` is authoritative. Existing legacy trust files are read for migration only and remain untouched. Missing trust alongside a legacy version journal or trusted catalogue is an error, not permission to enroll a different root. Damaged version objects and invalid signing-key length/public half are rejected without overwriting them. Key generation creates only absent files with exclusive creation; complete key-rotation/recovery procedures remain separate work.

## 3. Pinned delivery and preparation

New routes are authenticated agent reads under `/api/v1/agent/releases/<digest>/tuf/<metadata>` and `/api/v1/agent/releases/<digest>/artifacts/<target>`. They require a committed catalogue inventory, a valid digest and an allowlisted metadata/target name. Unreferenced directories, unsigned extras and manifest downloads are not exposed. File opens are rooted in the release directory and checked for type/length. The agent verifies authenticated target hashes; the HTTP server does not rehash the entire file on every GET.

New update jobs begin as internal `preparing` records, unavailable to agent polling. All eligible target parameters, statuses and operation summaries are published transactionally only after release, platform and capability checks. A partial-plan write failure does not expose an earlier target's incomplete job. Existing lifecycle conflicts remain rejected, rather than being overwritten by a later preparation error. Interrupted preparation does not dispatch work; it expires under the original job deadline. It is not a fully implemented durable rollout-resume engine.

Jobs pin `release_digest`, name, length, SHA-256, OS/architecture and previous session. The new worker advertises `immutable_release_v1` and fetches both metadata and bytes from that exact release prefix. No fallback from malformed pinned identifiers to mutable URLs is allowed. Metadata expiration and the agent's accepted-version journal still apply: immutable bytes are not permission to replay old metadata. Existing explicitly journal-authorized local rollback remains separate from requesting an older signed release.

An agent-side integration test uses actual TLS and signed repositories. After publication of B, a fresh compatible agent downloads A; it then accepts B and rejects replay of A's older metadata. Tampered B bytes fail even when the command supplies their matching hash, because the authenticated target metadata disagrees. Failure leaves the previous verified staging file unchanged. Fixture bytes are not executed.

## 4. UI and migration boundary

Updates shows immutable versus legacy catalogue rows, digest, signed metadata expiry, per-agent compatibility and inline progress/errors. Selecting a target is preflight only; server and worker validate again. A reconciled still-pending import is not described as already published. A queued installation is not described as completed.

**One-time transition requirement:** deploy the matching server/UI first, then upgrade existing agents through the protected local install/update procedure so their capability report contains `immutable_release_v1`. Older agents continue monitoring normally, but are intentionally ineligible for new-style rollouts. Do not remove the gate or pretend the previous downloader understands the new URLs. No re-enrollment, credential replacement or endpoint reset is needed.

Legacy catalogue rows are visible but cannot start new rollouts. Re-import a valid signed bundle to create a new immutable entry. Legacy shared files remain frozen/untouched so already-issued legacy jobs are not redirected to new content; they retain their original expiry and other limitations. If metadata has expired, issue a new properly signed bundle with increasing metadata versions rather than deleting high-water state or turning off verification. Back up the database AND the release directories/trust material; a database-only backup is not enough.

## 5. Executed validation and limitations

| Check | Current result |
|---|---|
| Full Go race suite | PASS: 254 top-level tests, 322 test/subtest pass events, 19 tested packages |
| Go vet / module verification | PASS |
| Frontend | PASS: 96 Node tests (helpers/request logic, not full browser acceptance) |
| TypeScript / Vite | PASS: no-emit type check and production build; not separate vue-tsc |
| Linux amd64 | All four commands compiled |
| Windows amd64 | All four commands cross-compiled; native execution NOT RUN |
| Compiled Linux worker + supervisor | 2 separate PASS runs under UID 65534: actual CPU/RAM, stop/restart/respawn, refused uploads, spool recovery, identity/URL continuity |
| Exact signed-release HTTP/TLS paths | PASS: server API and actual agent downloader tests, not native installation of fixture bytes |
| SQL failure injection | PASS: artifact/result/second-plan-target faults cannot partially publish database state |
| Actual browser attempt | BLOCKED before login by ERR_BLOCKED_BY_ADMINISTRATOR on isolated loopback |
| Browser scenario | Extended Updates checks; Python syntax PASS; current browser assertions NOT EXECUTED |
| Native systemd/SCM boot, power loss, service-host self-update | NOT RUN / independent recovery still NOT IMPLEMENTED |
| Fleet capacity, 24h soak, full protected restore, whole v3 battery | NOT RUN / outstanding implementations remain |

The two opt-in compiled-process tests are skipped in the ordinary Go suite and executed separately. Go 1.27.0 and Node 22.16.0 were used with the repository's locked dependencies. The first intermediate full suite revealed a lifecycle-conflict outcome regression introduced during this change; the implementation was corrected without weakening that existing test. The final full suite passes.

## 6. Remaining P0 work, not hidden by this pass

Native independently recoverable service-host replacement; complete systemd/SCM installation and interruption rollback; persisted canary/platform cohorts and bounded rollout batches; pause/resume/retry orchestration; full protected controller backup/restore and stale-job reconciliation; signing-root/key rotation; long-offline expiry/recovery; fair bounded downloads without blocking control; release retention/orphan accounting; long-term metric aggregates and measured capacity. `operation.retry_selected`, `update.resume` and service-host self-update remain unavailable where their real implementations are incomplete.

Do not turn temporary update limitations into a silent monitored-service outage. Keep the current worker and its protected state until an independent completion/rollback proof exists. Existing operation read/unread, TV layout, selective monitoring, names and SSH separation remain unchanged.

## 7. Sources

Product decisions/tests above are source observations, not certifications by upstream projects. Primary constraints checked on 2026-09-14: TUF specification 1.0.36 (`https://theupdateframework.github.io/specification/latest/`), SQLite transactions (`https://www.sqlite.org/lang_transaction.html`), Go rooted filesystem APIs (`https://pkg.go.dev/os#Root`). TUF authenticates distribution data; it does not implement the native installer or recovery mechanism.
