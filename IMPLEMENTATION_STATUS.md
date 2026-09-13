# Implementation status after source audit

Audit baseline: `2515a91b34d3cbbd1b4a68f74513b5462c53145c`, 2026-09-13.

**Pre-release. NOT a complete v3 implementation.** Earlier labels saying signed updates and safe rebind were implemented overstated the evidence. See `docs/AUDIT_2026-09-13.md` and `ACCEPTANCE_LEDGER.md`.

| Area | Actual status |
|---|---|
| Go server, SQLite, Vue build | Implemented baseline; tested in isolation |
| Five-second telemetry and local HTTP checks | Implemented with corrected atomic ingestion/replay; broader collector scheduling/coverage remains partial |
| Overview and machine drill-down | Corrected CPU/RAM/DISK/ping/services and raw-history graphs; visual CI/native acceptance pending |
| Durable operations and honest feedback | Corrected delivery/receipts/idempotency/expiry; unsupported actions explicitly rejected; full config CAS remains open |
| Signed worker and service-host updates | **BLOCKED / NOT RELEASE-READY**; unsafe import/activation paths quarantined; mandatory native implementation remains |
| Safe controller rebind | **BLOCKED / NOT IMPLEMENTED END TO END**; blind URL replacement removed |
| Historical state | Raw 48h queries/point lookup/extrema corrected; long-term aggregates, complete versioned rules and service history remain partial |
| Secret delivery, maintenance, trust/credential rotation | Unimplemented public actions reject, not fake success |
| Backup/restore | Consistent DB snapshot helper; NOT verified full-controller backup/restore |
| TLS renewal | SAN-preserving, serialized, atomic leaf bundle; Linux race/regression tests pass; Windows native durability not tested |
| Native deployment | Linux/Windows cross-builds pass; this audit did NOT verify installation/reboot or updater recovery |
| Load/soak | NOT RUN |

Default initial controller URL stays `https://46.120.103.61:8777`. Persisted addresses win. Telegram is deferred. No production machine was accessed or modified by this audit. No unrelated project is in scope.

Do not enable update/rebind guards until real implementation and adversarial native tests replace the missing paths. Successful helper/unit tests and a compiling Windows binary do not satisfy those gates.
