# Current implementation: V13 reliability audit, PRE-RELEASE

Base: `5fef10f15b610ff79b036f040d45037175b28866`. Read `docs/AUDIT_REVIEW_13_2026-09-15.md`, `docs/V13_RELIABILITY_RU.md` and `ACCEPTANCE_LEDGER.md`.

Implemented this pass: complete bounded control/secret-response JSON, duplicate-member health rejection; no startup fallback from lost initialized config; current-check provenance for UI/incidents; durable receipt-ACK removal and fair bounded receipt delivery; immutable queued observations; read-side queue retention and durable loss ledger; non-live backfill independent from old job receipts; last known address preservation and safe controller-origin path joining.

All integrated monitoring/UI/auth/SSH/immutable-release/canary features are retained. New wire schema, service privileges and package dependencies were not introduced. No GitHub write or live deployment by the auditor. Host re-ran the sealed gates and executed the blocked browser scenario (31/31, UID 1000 native). Unit/helper/fault tests are not complete release evidence; exact validation is recorded separately. Historical status through V12 is in `docs/history/IMPLEMENTATION_STATUS_THROUGH_V12.md`.

Open release gates: native systemd/Windows SCM installation/boot and Windows rights/IPC; independent service-host self-update recovery; protected complete backup/restore/reconciliation; native multi-machine rollout; long-term aggregates/coverage, storage capacity/soak and physical TV/SSH-shell acceptance. Generic selected retry and independent service-host updates remain explicitly unavailable. See the authoritative consolidated `docs/RELEASE_COMPLETION_PLAN.md`.
