# Monik implementation status: Audit5 source

Reviewed base `37dc23405606b24a82a629533311c19dae577a2a`. **PRE-RELEASE.** Code and bounded tests are not native fleet acceptance. See `docs/AUDIT_REVIEW_5_2026-09-14.md`, `docs/AUTO_ENROLLMENT_RU.md`, and `docs/RELEASE_COMPLETION_PLAN.md`.

| Area | Current state |
|---|---|
| Overview/header | Requested alignment/noise removal, platform under name and unread-only incident count implemented; health/pins unchanged |
| Services/editor | Machine-group disclosures, search, explicit selected-editor opening/scroll/focus, history-error isolation; synthetic browser 14/14 PASS on this host |
| Rename | UI, API validation/name-value CAS, stored label survives telemetry; bounded tests pass |
| Agent arrival | Trusted profile and quarantined outbound worker loop; owner-only recent-auth approve/reject; real local HTTPS test passes |
| Native installation | Existing installer account/ownership/ACL gaps remain; new beacons require an actually running service or foreground process |
| Previous features | Audit4 custom checks, periodic scheduler, structured response advice, history/axes/export/operations retained |
| Updated verification | 158 top-level Go tests, 216 pass events, 18 packages; 47 Node tests; vet/tsc/UI and Linux/Windows builds pass |
| Visual/native scope | Synthetic browser 14/14 PASS on this host; Windows cross-build not SCM evidence; systemd/SCM boot/recovery and full fleet NOT RUN |
| Mandatory unresolved | Independent service-host update recovery, immutable releases/cohorts, full protected restore, long aggregates, effective rules/maintenance |

Do not reset the actual deployment URL or re-enroll current machines. Server first for new announcement routes, then compatible agent. Pending agents cannot be downgraded to a worker unaware of quarantine. Telegram, remote shell and host reboot remain out of scope. Publishing this source is not production deployment.
