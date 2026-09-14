# Monik implementation status: Audit 6 source

Base: `e771f35343b0c3f9aaba7db446d2ffd1afc3aa97` / tree `a4e92aa2286f3540982463768bd45752fe025d5c`. **PRE-RELEASE.** See `docs/AUDIT_REVIEW_6_2026-09-14.md`, `docs/V6_OPERATIONS_RU.md`, `ACCEPTANCE_LEDGER.md` and `docs/RELEASE_COMPLETION_PLAN.md`. Earlier audit documents are historical, not current acceptance status.

| Area | Actual implementation and remaining scope |
|---|---|
| Existing monitoring/UI | Dense pinned overview, grouped services, explicit editor opening, rename, chart axes, custom HTTP requests/advice and bounded raw history/export retained |
| Host rules | Effective global CPU/used-RAM/most-filled-disk percent rules, CAS versions, persistence/recovery/hysteresis and policy-change evidence implemented; per-host/other metrics not implemented |
| Maintenance | Durable fleet/agent/service API windows, cancel/history, current/evidence markers; fleet+machine creation UI. Measurements continue; repeating/editable schedules not implemented |
| Automatic enrollment | Owner-controlled expiring window for unknown identities; default closed; existing pending/approved and one-use codes unaffected; native installer gaps remain |
| Incident attention | Unread filter, audited unacknowledge, critical escalation clears read once; peak severity retained; no acknowledgement-as-recovery |
| Account/session | Recent-auth dialog resumes original key once; owner password change revokes all browser sessions atomically; stale-login/session races covered |
| Storage | Corrupt/null service observations return errors, indexed diagnostics, bounded cleanup/event resync; long-term aggregates and large-fleet performance not accepted |
| Operation evidence | New policy commit/result-write uncertainty stays unknown; unsupported retry_selected/update.resume remain rejected |
| Native lifecycle | Independently recoverable service-host self-update NOT implemented; worker update has partial tested paths, native install/reboot/power-loss not accepted |
| Updates/rebind/restore | Full immutable catalogue/cohort rollout, physical migration/recovery and protected controller restore remain release blockers |
| Verification | Go race 178 top-level / 239 test+subtest events / 18 packages; 57 Node tests, vet, module verify, tsc, UI pass on this host (Go 1.27.1) |
| Browser/native evidence | This host: Playwright 19/19 on the synthetic fixture plus disposable loopback smoke for rules/maintenance/admission. Auditor browser was blocked before login. Windows builds are cross-compilation only; systemd/SCM/soak NOT RUN |

Unknown automatic registrations now require opening the admission window. Server/UI first, then compatible workers. Existing identities, trusted configuration and selected controller URL must be preserved. Rules/maintenance/admission are NOT enforced by a pre-v6 server after downgrade. Publishing source does not deploy it.

The bootstrap URL remains `https://46.120.103.61:8777`; it never overrides an existing selected endpoint. Telegram, generic remote execution and host reboot remain excluded.
