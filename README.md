# Monik v2

> Latest source: **Audit 4, pre-release**. Custom check editor and bounded health advice: [audit](docs/AUDIT_REVIEW_4_2026-09-14.md), [operator guide](docs/CUSTOM_SERVICE_CHECKS_RU.md), [integration](docs/GROK_AUDIT4_HANDOFF.md). New server + capable worker required. Native lifecycle and full restore remain release blockers.

Self-hosted host and local HTTP/HTTPS service monitor: one Go server with an embedded Vue 3 UI and SQLite, plus native Linux and Windows agents.

Default bootstrap URL: `https://46.120.103.61:8777` (configuration value; existing persisted addresses always win).

Russian UI is the default. See `README_RU.md` for the owner walkthrough.

> **Pre-release:** the source audit found incomplete update/rebind and other management handlers. Unsafe paths now reject rather than claim success. Signed worker/service-host updates and safe rebind remain mandatory release blockers. Read `docs/AUDIT_2026-09-13.md`, `IMPLEMENTATION_STATUS.md` and `ACCEPTANCE_LEDGER.md` before deploying. This source change does not update a running installation.

## Layout

| Path | Role |
|---|---|
| `cmd/monik-server` | Controller, UI, SQLite, TUF mirror |
| `cmd/monik-agent` | Collector / control worker |
| `cmd/monik-service-host` | Narrow supervisor; native safe update lifecycle NOT accepted |
| `cmd/monik-release` | Offline TUF key init, sign, pack |
| `web/` | Vue 3 + TypeScript source |
| `internal/webui/dist` | Generated embedded UI; build before compiling the server |

## Build

Requires the Go version declared in `go.mod` (currently 1.27.0) and Node 22 for the tested build. `make all` builds frontend assets before the server. Generated assets are no longer committed; a server compiled without `make ui` returns an actionable 503 for the UI. The Makefile prepends `$HOME/.local/go/bin`, so verify `go version` when using a different toolchain.

```bash
make ui
make linux
make windows   # cross-compile amd64
make test
```

Linux binaries land in `dist/linux-amd64/`. Windows agents in `dist/windows-amd64/`.

## Server

```bash
./dist/linux-amd64/monik-server setup --non-interactive \
  --listen 0.0.0.0:8777 \
  --advertised-url https://192.168.12.128:8777
./dist/linux-amd64/monik-server run
```

HTTPS is the default. The controller uses a private CA with IP SANs. Admin password is written to `$MONIK_DATA/admin-bootstrap.txt` (mode 0600). Data default: `~/.local/share/monik-server`.

## Agent

Copy `monik-agent` and `monik-service-host` to the target. Create a one-use enrollment profile from the UI (**Добавить машину**) and run:

```bash
./monik-agent setup --profile enrollment.yaml
sudo ./monik-agent service install --config /var/lib/monik-agent/agent.json
```

Foreground `monik-agent run` is unmanaged. Remote update/restart currently remain unavailable even with the service host installed until the missing native lifecycle is implemented and tested. Installation commands must be validated against the actual target paths and privileges before fleet rollout.

## Signed updates: mandatory, currently blocked

The signing/helper tooling below is not a complete trusted update client or native rollback proof. Public import/activation paths are blocked until the trusted-root and lifecycle gaps in the audit report are fixed. Do not use it as a deployment shortcut. Root and targets private keys must never live on the running server.

```bash
./monik-release init --keys ./release-keys
./monik-release sign --keys ./release-keys --repo ./tuf-repo \
  linux-amd64/monik-agent=./dist/linux-amd64/monik-agent \
  linux-amd64/monik-service-host=./dist/linux-amd64/monik-service-host
./monik-release pack --repo ./tuf-repo --output monik-release.tgz --version 0.1.0
```

The UI displays the incomplete status; it cannot import or activate a bundle in this audited pre-release. Native Windows service installation/reboot and crash recovery are not validated by cross-compilation.

## License

Apache-2.0
