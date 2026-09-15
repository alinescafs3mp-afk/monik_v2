# V17: resizable Overview and frontend reliability, PRE-RELEASE

Baseline `35fccbb0265777b12c2893667ef2fea33e953928`. Current audit: `docs/AUDIT_REVIEW_17_2026-09-15.md`; prior host evidence is preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V16.md` and prior status in `docs/history/IMPLEMENTATION_STATUS_THROUGH_V16.md`. Host validation: `docs/audit/validation-review17-2026-09-15.json`.

Implemented: shared draggable/keyboard-accessible adjacent column boundaries; mode-local persisted widths and reset; responsive service reflow and mobile fallback; stable host order during drag; latest-selection graph point provenance; honest export readiness; consistent live service priority; measured TV footer and invalidated row-height cache; cross-machine secret draft clearing.

No production Go/schema/protocol/dependency change. All previous monitoring, enrollment, immutable update, scoped SSH and safety guards remain. Host executed the V17 browser geometry suite (39/39) that the auditor could not log in to. Physical TV/Yandex, native service boot, independent service-host recovery, complete restore, long-term aggregates and large-fleet soak remain separate open gates. This is not a declaration of full v3 completion.
