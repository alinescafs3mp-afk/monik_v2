# Monik

Self-hosted monitoring for the owner's machines and local HTTP/HTTPS services: one Go controller with embedded Vue/TypeScript UI and SQLite, plus native agent/limited service-host builds.

**Current source: Audit 6, PRE-RELEASE.** Read [the actual status](IMPLEMENTATION_STATUS.md), [acceptance ledger](ACCEPTANCE_LEDGER.md), [audit](docs/AUDIT_REVIEW_6_2026-09-14.md) and [remaining release gates](docs/RELEASE_COMPLETION_PLAN.md). Source changes never imply the running installation has been deployed.

The configurable bootstrap URL remains `https://46.120.103.61:8777`. An existing selected/persisted controller URL always takes precedence. Russian UI is default. [Operator guide](docs/V6_OPERATIONS_RU.md).

## Working scenarios

Dense pinned-machine overview with CPU/RAM/disk/ping and service evidence, grouped service inventory, persisted machine names, labeled raw history and bounded export; local discovery and explicit periodic HTTP requests with trial/secret references; durable operations and truthful partial/unknown outcomes. Audit 6 adds real global host thresholds, maintenance intervals/cancellation, an owner-controlled automatic admission window, incident unread controls, recent-auth dialog, password change/session revocation and bounded storage diagnostics/cleanup.

Unknown automatic-profile agents are admitted to the pending queue ONLY while the owner opens the admission window on Add machine. Closing it does not strand an existing pending/approved identity. One-use enrollment codes and authenticated agents are independent of the window. The owner must still verify/approve candidates. [Automatic enrollment](docs/AUTO_ENROLLMENT_RU.md) describes the base flow; [v6 changes](docs/V6_OPERATIONS_RU.md) add the admission gate.

## Build

Use the Go version in `go.mod` and the locked frontend dependencies. Audit 6 used Go 1.27.0 and Node 22.16.0. Build the UI before the Go controller; generated assets and dependencies are not committed.

```bash
cd web
npm ci
npm test
npx --no-install tsc --noEmit
npm run build
cd ..
go test -race -count=1 ./...
go vet ./...
make dist
```

`make dist` normally rebuilds UI. `make dist -o ui` is appropriate only after building the matching production UI separately. The Makefile can prepend `$HOME/.local/go/bin`; check the actual compiler selected. Linux and Windows amd64 outputs are in `dist/`. Cross-compilation is not native service acceptance. The browser CI fixture is synthetic telemetry, not a native fleet.

## Deploy deliberately

Server/UI first, then compatible agents, preserving identities, protected state, keys and the actual selected endpoint. Take a complete protected backup before schema/index changes and test on a populated copy. Do not blindly run setup over an existing enrollment. Use CLI help for the chosen build and paths rather than assuming a sample data path matches your service account.

Linux service-account/ownership provisioning, Windows restricted identities/ACLs, native boot and independent service-host self-update recovery remain incomplete/not accepted. A running old supervisor is not proof a replacement will boot. Worker update and rebind contain tested partial implementations, not a finished fleet rollout. Immutable release publishing/cohort resume, full controller restore/reconciliation and long-term aggregates remain release blockers. Some corresponding APIs deliberately reject unfinished actions; do not remove guards to make buttons green.

No remote shell, host reboot or unrelated application/container control. External Telegram/email notifications remain deferred. Keep signing private keys and production runtime state out of this repository and ordinary diagnostics.

## Layout

`cmd/monik-server`, `cmd/monik-agent`, `cmd/monik-service-host` and `cmd/monik-release` build the four programs. `internal/` contains protocols, storage, checks and lifecycle modules. `web/` contains UI source. `tests/browser/` and `tests/fixtures/` provide the synthetic browser scenario.

License: Apache-2.0; see LICENSE.
