# Acceptance ledger: audited source

Base reviewed: `2515a91b34d3cbbd1b4a68f74513b5462c53145c`, 2026-09-13. This ledger does not carry forward unverified deployment/UI claims from the first source revision.

| Check | Result | Evidence/scope |
|---|---|---|
| Go tests with race detector | PASS | 55 top-level tests / 73 test and subtest pass events in 12 packages; `go test -race -count=1 -json ./...` |
| Static Go checks | PASS | `go vet ./...` |
| Frontend request/feedback regressions | PASS | `cd web && npm test`: 9 Node tests |
| TypeScript | PASS | `tsc --noEmit`; no separate vue-tsc claim |
| Vue production build | PASS | `npm run build` |
| Linux amd64 binaries | PASS | all four commands, CGO disabled |
| Windows amd64 binaries | PASS | all four commands; compile only |
| Browser script syntax | PASS | Python compile check |
| Local browser walkthrough | BLOCKED | `ERR_BLOCKED_BY_ADMINISTRATOR` on isolated loopback fixture; visual acceptance NOT RUN |
| CI browser fixture | PASS | `tests/browser/audit.py` against compiled `tests/fixtures/audit-server`; 7 checks; synthetic loopback, not native-fleet evidence |
| Native Windows service/reboot | NOT RUN | Compilation is not SCM acceptance |
| Signed update security and native crash recovery | PARTIAL | Enrolled TUF root, confined activate/rollback and Linux unit tests exist; native Windows crash recovery and 24h soak NOT RUN |
| Prepared/confirmed rebind with offline agents | PARTIAL | prepare/arm/activate/retire jobs; unprepared activate rejects; native multi-host cutover NOT RUN |
| Credential rotation overlap | PASS (unit/integration) | Next token persisted first; pending verifier accepted; old hash retired after the new token authenticates |
| Controller trust stage/retire | PASS (unit/integration) | Extra PEM staged; retiring the last root is rejected |
| Trial check without controller SSRF | PASS (unit/integration) | Controller does not dial; agent runs one GET/HEAD under local policy; definition not saved |
| Full-controller backup/restore | NOT ACCEPTED | Database snapshot is not a protected full-controller restore |
| 24h soak / 50-agent capacity | NOT RUN | No capacity claims |
| Owner's live server | NOT ACCESSED | Source audit only; no credentials used or deployment changed |
| Complete v3 acceptance battery | NOT RUN | Mandatory blockers listed in audit report |

Machine-readable evidence: `docs/audit/validation-2026-09-13.json`. Browser screenshots/results, when the new CI run completes, are separate GitHub Actions artifacts. Never relabel synthetic fixture outcomes as native-agent evidence.
