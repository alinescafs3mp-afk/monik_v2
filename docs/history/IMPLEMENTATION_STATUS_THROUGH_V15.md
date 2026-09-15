# V15 current implementation: one-file Linux installer, PRE-RELEASE

Base: `ae6c5822843ec74fd4a0838913f1291849691d00`. Read `docs/AUDIT_REVIEW_15_2026-09-15.md`, `docs/V15_ONE_FILE_AGENT_RU.md` and `ACCEPTANCE_LEDGER.md`.

Implemented: credential-free reusable installer templates; owner-authenticated HTTPS download of a single ELF containing matching worker, supervisor, selected origin, public CA and a one-hour one-machine enrollment code; evidence-based `ГОТОВО` (enabled+active systemd unit, supervisor/worker hashes, two fresh server-confirmed reports); source bytes of both binaries read before any mutation; unambiguous JSON/profile parsing; browser fixture retries the same observation only on SQLite BUSY/LOCKED.

Reusable templates contain no enrollment secret. Existing LAN agents and the saved advertised URL are unchanged. Windows and non-systemd Linux keep the previous advanced path. `operation.retry_selected` and independent service-host self-update stay fail-closed.

Host source gates: 388 top-level Go/fuzz-seed targets, 536 passing test/subtest/seed events across 26 packages under race; 123 Node tests; tsc/Vite; vet/modules; 8 cross-platform application builds plus installer templates; 3 compiled Linux process scenarios including signed update/failed-candidate recovery; V15 cases repeated ten times; bundle fuzz 15s 11,469,734 execs; Playwright 34/34. systemd/boot NOT RUN here.

Open release gates remain: native systemd/Windows SCM boot on a real first host, independent service-host replacement/recovery, protected complete restore/reconciliation, actual multi-host rollout, long-term history/coverage/capacity and soak/physical-TV acceptance.

# Current implementation: V14 inventory and external profiles, PRE-RELEASE

Base: `b969c6a34d3852687bd3aa3c73212ec4e183f55f`. Read `docs/AUDIT_REVIEW_14_2026-09-15.md`, `docs/V14_DISCOVERY_AND_CONNECTION_RU.md` and `ACCEPTANCE_LEDGER.md`.

Implemented: corrected external bootstrap; separately chosen new-agent profile origin with active-leaf SAN/validity preflight; explicit locked OFFLINE `tls-add-name` retaining existing CA and old names; SAN-preserving renewal. Existing saved agent/controller routes do not change.

Versioned complete local listener snapshots independently describe presence despite HTTP errors/probe budget/disabled checks. Transactional, replay-aware two-scan/60-second absence tracking hides missing unmonitored unpinned discoveries from normal views, without deleting names, checks or history. Expected/selected services remain visible through failures. Show-disappeared views and counts are provided. New baseline checks publish one config revision per accepted batch. Linux parser errors no longer become false complete inventories; Windows IPv6 owner-PID offsets/bounds are corrected.

All V13 monitoring, control, auth, SSH, immutable releases and wave scheduling are preserved. `operation.retry_selected` and independent service-host update stay fail-closed. The wire schema remains 3 with additive optional inventory fields. An old agent cannot establish absence. Server/UI must be upgraded first.

Final source gates: 353 top-level Go/fuzz-seed targets, 478 passing test/subtest/seed events across 23 packages under race detector; 115 Node tests; tsc/Vite; vet/modules; all 8 cross-platform builds; 3 compiled Linux process scenarios including signed update/failed-candidate recovery. V14 cases repeated ten times with race/shuffle. Four baseline regressions reproduced. Windows fixtures/fuzz do not establish native Windows API behavior. Auditor browser BLOCKED_BEFORE_LOGIN; host executed 33/33 (uid 1000 native). Host extra: wrap Add-machine install-command `<pre>` so the existing 390px overflow assertion holds; the assertion was not weakened.

Open release gates remain: native systemd/Windows SCM boot and restricted rights, independent service-host replacement/recovery, protected complete restore/reconciliation, actual multi-host rollout, long-term history/coverage/capacity and soak/physical-TV acceptance. No source audit result authorizes a blind fleet deployment.
