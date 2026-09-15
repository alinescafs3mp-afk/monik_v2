# V18: outbound agent terminal and TV remote column control, PRE-RELEASE

Baseline `afdbbd2573b68d99eb3115032f62f6b33681a5cd`. Current audit: `docs/AUDIT_REVIEW_18_2026-09-15.md`; prior host evidence is preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V17.md` and prior status in `docs/history/IMPLEMENTATION_STATUS_THROUGH_V17.md`. Host validation: `docs/audit/validation-review18-2026-09-15.json`.

Implemented: agent-initiated WSS console under a separate unprivileged `monik-console` account; local root `console-enable`/`console-disable`; installer opt-in unchecked by default; protected SSH setup with CAS/fingerprint; TV OK/arrow and native +/− column controls. V17 column geometry and V16 silent refresh remain.

This is not a root/admin shell, ConPTY, generic durable runner, or independent service-host self-update. Host executed the V18 browser suite (41/41) that the auditor could not log in to. Native systemd socket activation, cgroup descendant cleanup, boot, physical TV/Yandex, complete restore and large-fleet soak remain separate open gates. Do not enable the console on the live controller VM or the first remote without a disposable Linux/systemd pilot. This is not a declaration of full v3 completion.
