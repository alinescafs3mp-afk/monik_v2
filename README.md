# Monik v2

Self-hosted host and local HTTP/HTTPS service monitor: one Go server with an embedded Vue 3 UI and SQLite, plus native Linux and Windows agents.

Default bootstrap URL: `https://46.120.103.61:8777` (configuration value; existing persisted addresses always win).

Russian UI is the default. See `README_RU.md` for the owner walkthrough.

## Layout

| Path | Role |
|---|---|
| `cmd/monik-server` | Controller, UI, SQLite, TUF mirror |
| `cmd/monik-agent` | Collector / control worker |
| `cmd/monik-service-host` | Narrow supervisor for crash-safe updates |
| `cmd/monik-release` | Offline TUF key init, sign, pack |
| `web/` | Vue 3 + TypeScript source |
| `internal/webui/dist` | Embedded production UI |

## Build

Requires Go 1.24+ (`$HOME/.local/go` is used by the Makefile) and Node 20+.

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

Foreground `monik-agent run` is unmanaged: remote update/restart are unavailable until the service host is installed.

## Signed updates

Root and targets private keys never live on the running server.

```bash
./monik-release init --keys ./release-keys
./monik-release sign --keys ./release-keys --repo ./tuf-repo \
  linux-amd64/monik-agent=./dist/linux-amd64/monik-agent \
  linux-amd64/monik-service-host=./dist/linux-amd64/monik-service-host
./monik-release pack --repo ./tuf-repo --output monik-release.tgz --version 0.1.0
```

Import the bundle in the UI. Import is catalog verification, not installation.

## License

Apache-2.0
