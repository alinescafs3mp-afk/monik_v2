# Deployment and recovery: actual supported boundary

**Pre-release source. Read the current acceptance ledger first.** Existing status documents are not proof of native service install, full-controller restore or safe fleet-wide rollout. This run did not modify the owner's installation.

## Safe integration order

Preserve the current Git worktree and protected running state, then apply exactly one route from the cumulative audit package. Build the embedded UI before the server. Run regression tests, browser fixture and a disposable native server/agent pair. Publish tested source separately from deployment; a main commit is not permission to restart the whole fleet.

The controller's actual configured HTTPS origin must remain unchanged. The original bootstrap constant is only a first-run default. A new agent retains the explicitly selected URL rather than trusting an advertised stale address to relocate it.

Deploy the audit-3 server before enrolling new audit-3 agents. Existing enrolled agents retain credentials and identities. On a lost enrollment response retry the same unexpired profile; a protected enrollment intent contains the persisted identity/proof. Do not run setup over an existing configuration or delete uncertain state before checking its registration.

## Backup is more than one database file

The current `monik-server backup` helper creates a consistent SQLite snapshot. It is **not a complete protected-controller archive**. The restore helper does not supply all original TLS/CA identity, secret-encryption keys, settings, update trust/high-water state, job receipts and current-agent reconciliation by itself. Do not present the DB-only restore as a safe controller migration or copy the active .db without its WAL/backup procedure.

Before a real deployment, arrange an owner-protected, consistent copy of all required controller and selected-agent state. Include TLS material, credentials, applied config/envelope, enrollment intent if pending, lifecycle journals and both spool formats. Never commit or attach these live files to an audit report. Implement and prove the full procedure in `RELEASE_COMPLETION_PLAN.md` before claiming disaster recovery.

## Agent native lifecycle

Foreground tests and cross-builds do not provision the native service identity or prove ACL/ownership. Validate Linux user/state ownership and Windows restricted service identity, before-login boot and native recovery. Keep the service-host self-update rejection until a genuinely independent native recovery path works.

New spool records are in `spool/records-v2`; the current worker reads both queues. An older worker leaves v2 records untouched and cannot drain them until re-upgrade. Keep adequate disk reserve for a staged rollback, preserve both formats and test the supported version pair. Loss counters are not yet a durable cross-restart ledger.

## Updates and controller relocation

Test selected worker updates with independently enrolled signed metadata and exact digest. The full immutable publication/cohort/pause/resume workflow is not finished. Do not import over an active release or advertise a fleet-wide all-in-one rollout before it exists and passes its failure tests.

Rebind must retain authoritative identity/trust and the currently valid endpoint until candidate confirmation. A physical move also needs data transfer and single-writer fencing. Prepared/armed/confirmed states from tests are not high availability. Never use two independently writable copies of the controller database.

## Diagnostic exports

Historical JSON export is an authenticated, 24-hour-lived, bounded sample file from one entity and range. It does not contain complete controller recovery material and is not a backup. Do not expose the exports directory through a static file server. Delete/export artifacts only under their lifecycle policy, not while clients assume a permanent link.
