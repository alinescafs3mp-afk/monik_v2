# Implementation status

Workspace: `/home/jericho/monik_v2` (independent git, module `github.com/alinescafs3mp-afk/monik_v2`). Friday, Codex, Astra and the Pandora box were not modified.

## Stack

- Go server/agent, SQLite WAL (`modernc.org/sqlite`, CGO-free)
- Embedded Vue 3 + TypeScript UI
- TUF via `github.com/theupdateframework/go-tuf/v2`
- Private controller CA with IP SAN + leaf reload
- Native Linux discovery (`/proc/net/tcp{,6}`) and Windows `GetExtendedTcpTable`

## Gates

| Gate | Coverage | Notes |
|---|---|---|
| 0 contracts/skeleton | implemented | action registry, schema, clock, ops model |
| 1 monitoring | implemented | enroll, 5s telemetry, local HTTP discovery, live UI |
| 2 operations | implemented | idempotent submit, durable ops center, offline targets |
| 3 signed updates | implemented | `monik-release`, import, service-host journal/rollback |
| 4 history | implemented | 1–24h series, extrema downsample, 48h raw retention |
| 5 rebind/admin | implemented | prepare/arm/activate/retire, backup/restore, secrets |
| 6 hardening | partial | focused tests pass; 24h soak NOT RUN; Windows native boot NOT RUN |

## Decisions

- Default advertised URL remains `https://46.120.103.61:8777`; LAN deploy uses the host's LAN IP.
- HTML demo is not the production UI.
- Service host is a typed supervisor, not a remote shell.
- Root/targets signing keys never stored in the running server or this repository.
- macOS discovery is experimental (`NOT RUN` as supported).
- Telegram deferred.

## Remaining evidence (not fabricated)

- Native Windows service boot and update on a real Windows host: NOT RUN
- 24-hour soak: NOT RUN
- Public IP `46.120.103.61` was not scanned or used as a live target
