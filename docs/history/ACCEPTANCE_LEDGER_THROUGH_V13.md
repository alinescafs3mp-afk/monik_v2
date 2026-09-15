# Current acceptance: V13 reliability audit (2026-09-15)

Baseline `5fef10f15b610ff79b036f040d45037175b28866`. Read `docs/AUDIT_REVIEW_13_2026-09-15.md` and `docs/audit/validation-review13-2026-09-15.json`. Previous ledgers are preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V12.md`; they are not current-version acceptance.

| Check | Actual final evidence |
|---|---|
| Baseline | Clean ordered UI + Go race pass: 305 top-level, 401 pass events, 21 packages; baseline CI also succeeded. Initial overlapping UI/Go embed attempt is a recorded harness error, not a product regression. |
| Full final Go race | PASS: 326 top-level events including 2 fuzz targets, 442 test/subtest/seed pass events, 22 tested packages. 3 opt-in native cases skipped here and executed separately. |
| Negative regression cases | 13 named scenarios fail on unchanged baseline behavior and pass after correction; overlapping failure modes, not 13 independent vulnerability findings. Additional new-ledger directory-symlink self-review defect fixed/tested. |
| New regressions repeated | PASS: all TestV13 across 6 packages, 10 shuffled repetitions, race detector. |
| Parser fuzz | Auditor: 15s, 152,494 execs, 2 workers. Host: 15s, 2,353,331 execs, 2 workers. Bounded input; not exhaustive parser proof. |
| Native Linux processes | PASS 3/3. Auditor UID 65534; host UID 1000. Compiled binaries: real CPU/RAM, restart, offline queue, supervisor shutdown/respawn, signed worker update and failed-candidate recovery. Not systemd/SCM/boot. |
| Frontend / types / bundle | PASS: 108 Node helper/request/structure tests; tsc --noEmit; production Vue/Vite. Not separate vue-tsc or visual acceptance. |
| Vet / Go module integrity | PASS; dependency lockfiles unchanged. |
| Builds | Four Linux amd64 and four Windows amd64 programs; Windows compile-only. |
| Browser | Auditor BLOCKED_BEFORE_LOGIN (`ERR_BLOCKED_BY_ADMINISTRATOR`). Host executed: 31/31, including pending old-check result after config change (Services table + Machine `.service-row`). Fixture loopback only. |
| Native OS-service boot, power loss, physical TV, multi-host fleet, 24h soak | NOT RUN. |
| Complete protected restore, independent service-host update, long aggregates, generic selected retry | Still open/unimplemented; explicit guards retained where present. |

Auditor did not write GitHub or change production. Source/package checksums and patch-reproduction proof are provided outside the source archive to avoid circular self-hashes. Runtime test timestamps are kept as generated. A new local loopback pass does not authorize a blind mass rollout.
