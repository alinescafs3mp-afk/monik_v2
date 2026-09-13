# Implementation status

Source after remaining typed-action wiring on top of P0 lifecycle and audit integration `140446e`.

**Pre-release.** Native Windows SCM/boot, 24h soak and the owner's live LAN process are not acceptance evidence.

| Area | Actual status |
|---|---|
| Go server, SQLite, Vue build | Implemented; rebuild UI after this change |
| Five-second telemetry and local HTTP checks | Implemented with atomic ingestion/replay |
| Overview and machine drill-down | CPU/RAM/DISK/ping/services and raw-history graphs |
| Durable operations | Delivery/receipts/idempotency/expiry; typed registry actions are implemented. Fail-closed is empty: do not add a clickable action without tests |
| Signed worker and service-host updates | Implemented against an independently enrolled TUF root, confined staging, service-host probation and rollback journal. Native Windows crash-recovery and 24h soak are **NOT RUN** |
| Safe controller rebind | prepare/arm/activate/retire via jobs; prepare does not rewrite the controller URL; activate without a prepared plan is rejected. Offline unprepared agents cannot discover a replacement address |
| Secrets | Values stripped before persist; AES-GCM at rest; agent fetches by id over enrolled TLS. Historical plaintext from older builds is not rewritten by redaction |
| Credential rotation | Agent writes the next token first, registers its verifier over the old channel, then authenticates with the new token; the controller accepts a bounded overlap and retires the old hash |
| Controller trust overlap | `trust.stage` appends a PEM root on the agent; `trust.retire` refuses to drop the last remaining root |
| Check trial | One-shot GET/HEAD on the **agent** under the local probe policy. The controller never dials the trial URL |
| Native install | Linux copies host+worker into `/usr/lib/monik` and writes systemd; Windows service API is compiled. Reboot-before-login and Windows SCM acceptance are **NOT RUN** |
| Backup/restore | Consistent DB snapshot helper; NOT verified full-controller restore |
| TLS renewal | SAN-preserving leaf bundle; Windows native durability not tested |
| Load/soak | NOT RUN |

Default initial controller URL stays `https://46.120.103.61:8777`. Telegram is deferred. Applying this source must not restart the owner's live server as part of the source commit.
