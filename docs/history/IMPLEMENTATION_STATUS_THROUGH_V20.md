# V20: controller security/reliability hardening, PRE-RELEASE

Reviewed base `0385030878ec58e0f7a8bf1b95ab1f38fd1cde06`. Read docs/AUDIT_REVIEW_20_2026-09-15.md and docs/SECURITY_ACCEPTANCE_V20.md. Previous V19 status is preserved at docs/history/IMPLEMENTATION_STATUS_THROUGH_V19.md. Its local 45/45 browser report and the separate baseline GitHub CI failure after 42 passes are distinct executions.

Implemented: exact browser mutation origin policy, bounded password/SSE work, sensitive viewer-journal redaction, delayed-request role/token/session revalidation, transactional initial setup and credential revoke, safe pending credential promotion, explicit database read failures, narrow-TV CSS fallback and actionable browser-failure artifacts. Monitoring/console authority and agent protocol remain unchanged. Manual code issuance requires recent owner authentication.

Host executed `scripts/verify-audit20.sh --with-browser` (45/45) that the auditor could not log in to. V20 is not a guarantee of no vulnerabilities. Full protected restore, independent service-host recovery, physical TV, native service/boot and fleet/soak gates stay open. See ACCEPTANCE_LEDGER.md and docs/RELEASE_COMPLETION_PLAN.md.
