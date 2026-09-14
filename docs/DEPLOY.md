# Deployment: current V12 gate

The current authoritative deployment instructions are in [V12_DEPLOYMENT_GATE_RU.md](V12_DEPLOYMENT_GATE_RU.md) and [AUDIT_REVIEW_12_2026-09-15.md](AUDIT_REVIEW_12_2026-09-15.md). Run the source verification script and a permitted browser test, then a single native Linux systemd/reboot pilot with independent SSH access. No fleet-readiness claim follows from compilation alone.

## Recovery boundary

The existing DB-only backup/restore helper is not a full-controller protected backup or migration. Preserve the complete stopped data directory, original TLS/CA/master keys, all external configuration references, immutable releases, operation state and enrolled trust. One active writer only. Existing identities and the actual selected controller URL take precedence over defaults. Missing/corrupt keys or journals are now explicit stops, not automatic reset invitations.

## Updates

V10 immutable release publication and V11 frozen canary/batch plans are implemented. V12 corrects a real pending-update replay loop and verifies actual signed Linux worker replacement plus failed-candidate rollback. New-source worker and service-host should be deployed as a matched pair for the first pilot. Self-update of the service-host is still unavailable without independent native recovery. Never downgrade the controller during active held/paused v11-format plans.

## Agent and browser

Do not re-enroll existing machines. Keep both spool formats and applied configuration journals. A missing optional sensor is unavailable, not zero. Monitoring selection and overview pinning are independent. Read/unread never changes execution. SSH console uses protected fixed targets and explicit credentials, not the agent. The browser audit is a synthetic loopback fixture; physical TV/Yandex, mobile devices and native Windows services still require real tests.
