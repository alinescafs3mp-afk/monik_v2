# V16: first-host stability corrections, PRE-RELEASE

Baseline c671137d16269e568061a478b12fe3d4f3e7c82f. Current report: docs/AUDIT_REVIEW_16_2026-09-15.md. Previous status text: docs/history/IMPLEMENTATION_STATUS_THROUGH_V15.md.

Implemented: no passive editor scroll/focus, silent timer/SSE updates with explicit manual feedback, 20s fast-monitoring grace with monotonic server-anchored browser time, default HTTP-success lamp/summary and preserved custom assertions, left-anchored host facts and responsive three-row service columns, evidence-based ping diagnostics and same-target statistics, CAP_NET_RAW-only Linux unit fallback and explicit existing-unit repair. Rollout evidence retains the previous 15s bound. Existing enrollment, URL, TLS trust, checks, secrets and selection are not reset.

Read ACCEPTANCE_LEDGER.md for current evidence. Host executed the auditor-blocked browser suite (37/37) and loopback ICMP as uid=1000. systemd AmbientCapabilities, 8.8.8.8 from a managed unit, native boot and physical TV remain NOT RUN. No full-production-release assertion is made. Independent service-host self-update/recovery, full restore, native service boot, large-fleet/soak and long-term aggregate history gates remain open. No hidden activation of generic operation retry or remote shell through the agent.
