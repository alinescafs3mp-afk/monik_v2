# Monik implementation status: Audit 7 source

Base `5d17aaab6b3f4259820b089c28a7f4367e065372`, tree `1c2aaf9da364c541471bb45b9ec43bf3329a65d6`. **PRE-RELEASE; source package, not deployment.** Current evidence is in `docs/audit/validation-review7-2026-09-14.json`; audit in `docs/AUDIT_REVIEW_7_2026-09-14.md`; operator guide in `docs/V7_SCREEN_AND_MONITORING_RU.md`. Historical audit ledgers remain in Git history.

| Area | Implemented / remaining boundary |
|---|---|
| Service selection | Independent persistent overview pins and periodic-check controls; per-machine bulk pause/enable; original-socket discovery suppression on new workers |
| Discovery default | New primary checks paused; opt-in automatic monitoring with capability gate; limited initial identification remains active; existing checks preserved |
| TV/mobile | Explicit full-width TV rows, three densities, bounded paging and optional cycling; narrow controls/containers; physical Yandex/TV and remote-control acceptance NOT RUN |
| Login | Eye and native-manager autocomplete; opt-in 30-day server-backed persistent session, ordinary 12-hour session; logout/other-session revocation/expiry tests |
| Monitoring truth | Hidden/unpinned monitored failures still count; inventory-only/paused/progress not fabricated failures; history and all v6 rules/maintenance preserved |
| Storage | Cleanup time-budget yielding and interleaved bounded tables; native disk-loss/performance and long-term aggregates NOT ACCEPTED |
| Existing functionality | Grouped services, names, overview layout, chart axes, custom request/trial, admission, rules, maintenance, incident lifecycle, history/export retained |
| Native lifecycle | User/ACL provisioning and independent service-host self-update recovery remain incomplete, not merely untested |
| Release and recovery | Immutable multi-release publication, durable cohorts/resume, full protected backup/restore/reconciliation and long retention remain mandatory blockers |
| Browser | Host Chromium PASS 22/22 on the expanded scenario; auditor environment stayed BLOCKED_BEFORE_LOGIN. Previous 19/19 is baseline evidence only |

Server/UI first, then updated workers. Do not change identities, secrets, queues or the selected controller endpoint. New unknown registrations still need the existing admission window and approval. Telegram and general remote commands remain excluded. TV mode does not create a viewer-only security boundary.
