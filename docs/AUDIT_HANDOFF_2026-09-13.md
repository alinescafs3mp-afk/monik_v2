# Source audit handoff: corrective code is NOT merged

Date: 2026-09-13.

**Integration note:** this document is the information-only `aa83ae3` handoff and is kept on `main`. The corrective application code from `monik-audit-corrective-package-2026-09-13` is in the following commit. Signed worker/service-host updates and safe rebind remain mandatory release blockers; fail-closed guards were not removed. The owner's running installation was not changed by that integration.

**This is an information-only commit. It does NOT contain the corrective application code.** The OpenAI execution environment rejected a source-file write while publishing the tested changes. No incomplete application tree was attached to main. Repository write permission was available; this was not a GitHub permission error. The owner's running installation was not accessed or changed.

## Exact reviewed and corrected source

- Reviewed baseline commit: `2515a91b34d3cbbd1b4a68f74513b5462c53145c`.
- Reviewed baseline tree: `4c9ae60c066881562e5085aa581cea2896f49e3c`.
- Corrected, locally tested tree: `eb652e8fc9698d004f0d99210b24d319d5b69ab8`.
- Full corrective patch supplied to the owner in the conversation: `monik-audit-fixes.patch`.
- Patch SHA-256: `69e55d49ae091f98a9d617cb9a707b9020c5d09666d34c35d5fe9a02b7ed8dc7`.
- Corrected standalone source archive: `monik_v2-corrected-source.zip`.
- Source archive SHA-256: `9e6c4b4e5b662e126cff1dc830592f190f27136f8f3d35afdbef12d0076f229d`.
- The patch touches 83 paths, including removal of stale generated UI bundles. Applying it to the exact baseline was independently checked to reproduce the corrected tree above.

Do not confuse the existence of this report with deployment or completion of the fixes. The application source in this information-only commit retains its previous behavior. The complete patch, source archive, audit report and validation summary are in the owner's delivered package.

## Confirmed baseline problems

The existing tests passed, but did not cover several important defects:

1. History downsampling formed a recursive JSON object and could fail to return graph data. Variable-width timestamp strings could make point queries choose the wrong side of an exact-second boundary.
2. Public storage rows exposed credential-verifier/internal fields and did not consistently match the frontend's expected names. Overview cards did not provide the requested complete CPU/RAM/disk/ping/service summaries.
3. Report persistence was not all-or-nothing. Repeated envelopes could duplicate data; buffered host history and current contact were not reliably separated. Older sessions could rewind current metadata.
4. Marking a command delivered could remove it from subsequent polls before receipt, losing commands when responses were dropped. Receipt ownership and evidence checks were insufficient. Cancellation could imply already delivered work had been recalled.
5. Several actions claimed success without carrying out the required effect. A service-specific pause could pause broader host monitoring. Unsupported configuration values could appear applied.
6. Incident persistence/recovery did not enforce its advertised durations and service success/failure streaks.
7. HTTP body handling, configured expectations, IPv6 destinations and request methods needed bounded, truthful failure handling. Discovery/check work and backlog draining could delay current telemetry.
8. Update verification and native lifecycle were incomplete; trusting a root delivered by the candidate bundle is not independently enrolled trust. Rebind did not implement candidate verification, confirmed cutover and safe fallback.
9. Secrets could enter operation records; other management paths were unimplemented stubs. Existing plaintext history needs review and credential rotation where actually used, not merely read-time redaction.
10. Admin network restrictions, CSRF/logout, streaming reconnect/expiry and oversized request handling needed enforcement.
11. Browser mutations lacked reliable lost-response reconciliation and persistent feedback. A server acceptance was too easily mistaken for a completed agent action.
12. TLS renewal could lose IP/DNS SANs, raced with expiry reads, and used a separately replaced leaf certificate/key pair vulnerable to inconsistent files after interruption.

## Corrections prepared in the unmerged package

The package implements atomic report/deduplication transactions, owner-scoped observations and receipts, current/history separation, durable receipt retry and expiry, operation-specific completion evidence, conservative cancellation, configuration validation, service-only pause, observed-duration incidents, bounded checks and spool writes, and corrected history serialization/time ordering.

It adds real overview machine cards with CPU, RAM, the most-filled local disk, mean ping/loss and compact HTTP/application outcomes. Clicking a machine opens separate raw-history graphs with all six requested ranges, fixed HISTORY mode, point lookup, extrema and gaps. UI loading/error/unknown states and operation feedback are explicit. Lost mutation responses are reconciled through the original idempotency key rather than blindly submitting another action.

Unsafe unfinished update/rebind/secret/restart and other stub actions are quarantined with explicit errors at the relevant entry points. **This is NOT implementation of those mandatory release features.** The package also preserves certificate SANs and atomically publishes a complete private leaf bundle, removes stale generated frontend assets from Git, and adds regression tests plus a real-UI/synthetic-fixture browser scenario for CI.

## Executed validation of the corrective source, not of this report-only commit

| Check | Outcome |
|---|---|
| Go tests with race detector | PASS: 55 top-level tests, 73 test/subtest pass events, 12 tested packages |
| Go vet | PASS |
| Frontend request/feedback tests | PASS: 9 Node tests |
| TypeScript no-emit check | PASS; no separate vue-tsc claim |
| Production Vue/Vite build | PASS |
| Linux amd64 builds | PASS for server, agent, service-host and release tool |
| Windows amd64 cross-builds | PASS for all four commands; native execution NOT RUN |
| Python browser script syntax | PASS |
| Local browser walkthrough | BLOCKED: ERR_BLOCKED_BY_ADMINISTRATOR on the isolated loopback fixture; visual acceptance NOT RUN |
| New CI browser scenario | NOT RUN: scenario is in the unmerged patch |
| Native installation/reboot/update recovery | NOT RUN |
| Full v3 acceptance, fleet load and 24h soak | NOT RUN |
| Owner's production installation | NOT ACCESSED OR MODIFIED |

Go 1.27.0 and Node 22.16.0 were used with the reviewed revision's locked dependencies. Initial targeted regressions were observed failing before the corresponding fixes and passing afterward. The browser fixture uses a real Go API and compiled UI, but synthetic metrics; it deliberately does not pretend an agent command completed.

## Instructions for the local implementer

Obtain the delivered corrective package from the owner. Read its `README_ДЛЯ_ГРОКА.md` and `docs/AUDIT_2026-09-13.md`. Use a clean worktree and compare any intervening main changes before applying the patch. This information-only file does not conflict with the patch. Do not force-reset concurrent work or overwrite the repository with an extracted archive.

Run the patch preflight, apply and review it, rebuild the frontend before the server, run Go race tests/vet and frontend tests/type checking, then commit the integrated code to main. Run the added CI browser scenario and inspect its actual outputs. Do not assume its success from its presence in the repository.

Before deploying, back up the complete protected controller data/configuration. Test the timestamp migration on a database copy; it can hold a write lock on a large database. Preserve the entire TLS directory and controller secrets. Coordinate server and worker changes because an older worker's unsupported success claim will now be rejected. Generated UI assets are built by `make ui`/`make all`, not committed; a custom server build must include the built assets.

### Mandatory release blockers after applying the patch

- Independently trusted TUF metadata/targets and actual signed worker AND service-host update, probation, confirmation, batching and independent native recovery.
- Native Linux and Windows service install, boot before login, stop/reboot and crash-at-update-boundary evidence.
- Complete prepared/armed/ready/confirmed controller migration with scoped trust, fallback, offline accounting and endpoint retirement.
- Protected secret delivery without plaintext operation/audit payloads; review/rotation of legacy exposed values where relevant.
- Complete collector flags/intervals/deadlines, config base-revision CAS, check/vantage identity, versioned rules and maintenance.
- Actual long-term aggregation/retention, indexed historical service/incident queries and measured fleet capacity.
- Protected full-controller backup/restore and restored-job/single-writer reconciliation. A database-only snapshot is not proof of that.

Do not remove fail-closed guards merely to make buttons look functional. Telegram remains deferred. Do not add a remote shell, host reboot or unrelated application control. The release criterion is proven end-to-end behavior, not the number of screens or green helper tests.
