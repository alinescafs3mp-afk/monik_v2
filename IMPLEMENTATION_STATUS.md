# V19: shared TV layout profile, PRE-RELEASE

Baseline `776076273d677046b3a765e30d781f37b44957d5`. Current audit: `docs/AUDIT_REVIEW_19_2026-09-15.md`; prior host evidence is preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V18.md` and prior status in `docs/history/IMPLEMENTATION_STATUS_THROUGH_V18.md`. Host validation: `docs/audit/validation-review19-2026-09-15.json`.

Implemented: one controller-owned TV profile (five bounded column widths, density 10/12/14, autoplay) with versioned CAS saves, atomic audit/SSE, stale-write rejection and lost-response identity. Ordinary/compact widths, display-mode selection, fullscreen and all console/SSH state remain local. First publication is explicit; reconnect never republishes browser cache.

This is not a viewer/wallboard credential, not pixel mirroring, not a physical-TV/Yandex acceptance, and not independent service-host self-update. Host executed the V19 browser suite (45/45) that the auditor could not log in to. Native systemd/SCM boot, complete restore and large-fleet soak remain separate open gates. Existing agents were not replaced. This is not a declaration of full v3 completion.
