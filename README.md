## Current audit: V20

Existing-function and network-security hardening. Read [audit](docs/AUDIT_REVIEW_20_2026-09-15.md), [deployment notes](docs/V20_NETWORK_SECURITY_RU.md), [security acceptance](docs/SECURITY_ACCEPTANCE_V20.md) and [ledger](ACCEPTANCE_LEDGER.md). Run `scripts/verify-audit20.sh --with-browser`. V20 changes controller/UI only; preserve all enrolled state and rebuild matching installer templates for future downloads. Historical sections below are not current acceptance evidence.

## Historical V15 one-file Linux installation

Source for an owner-prepared single executable is implemented. See [the short user flow](docs/V15_ONE_FILE_AGENT_RU.md) and [validation boundaries](docs/AUDIT_REVIEW_15_2026-09-15.md). The auditor ran standalone installer/format tests, not the full Go 1.27 application or native systemd installation. Run `scripts/verify-audit15.sh --with-browser` and a clean-VM managed install/boot before distributing it. Preserve all existing controller/agent identity and trust. Previous audit sections below are historical.

## Historical V14 stabilization

Read [V14 inventory/address audit](docs/AUDIT_REVIEW_14_2026-09-15.md), [remote connection guide](docs/V14_DISCOVERY_AND_CONNECTION_RU.md) and [acceptance](ACCEPTANCE_LEDGER.md). Current inventory no longer grows forever with closed, unmonitored temporary ports. Expected/selected failures remain visible and history is retained. New external profiles can use the public origin independently of saved LAN routes. Run `scripts/verify-audit14.sh --with-browser` in a permitted environment; it does not install system services or deploy.

# Monik

Self-hosted monitoring for the owner's machines and local HTTP/HTTPS services: one Go controller with embedded Vue/TypeScript UI and SQLite, plus native agent/limited service-host builds.

**Current source: V20 controller security and reliability, PRE-RELEASE, visual/native release acceptance pending.** Read [the actual status](IMPLEMENTATION_STATUS.md), [acceptance ledger](ACCEPTANCE_LEDGER.md), [audit](docs/AUDIT_REVIEW_17_2026-09-15.md) and [remaining release gates](docs/RELEASE_COMPLETION_PLAN.md). Source changes never imply the running installation has been deployed.

The corrected new-profile bootstrap URL is `https://46.150.103.61:8777`. An existing selected/persisted controller URL always takes precedence. Russian UI is default. [Operator guide](docs/V7_SCREEN_AND_MONITORING_RU.md).

## Audit 10 release publication

Signed bundles now publish under immutable content-addressed paths with transactional catalogue/trust/results. New rollouts require `immutable_release_v1`; upgrade old workers locally once, preserving enrollment, then re-import valid signed bundles. Read [transition and limits](docs/V10_RELEASES_RU.md). Published files are not installation proof. Independent supervisor recovery, full restore, native cohort acceptance and long history remain open. Audit 11 adds the bounded controller-side rollout scheduler.

## Earlier Audit 7 additions

Service monitoring and display are independent controls. New discoveries remain paused by default; opt-in automatic monitoring requires `selective_monitor_v1`. Existing enabled checks are preserved. TV mode adds compact rows, device density and paging without browser zoom; login can retain a revocable 30-day session without storing the password in app storage. Current physical-TV/browser/native release acceptance is not claimed.

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

Linux service-account/ownership provisioning and one-file installer code exist; actual native install/boot must be accepted separately. Windows restricted identities/ACLs and independent service-host self-update recovery remain incomplete/not accepted. A running old supervisor is not proof a replacement will boot. Worker update and rebind contain tested partial implementations, not a finished fleet rollout. Immutable publication is now implemented; native cohort acceptance, service-host recovery, full controller restore/reconciliation and long-term aggregates remain release blockers. Bounded worker canaries/batches/pause/resume are now implemented; see docs/V11_ROLLOUTS_RU.md. Some corresponding APIs deliberately reject unfinished actions; do not remove guards to make buttons green.

No unrestricted or privileged shell through the monitoring agent, host reboot or unrelated application/container control. The separately authorized V18 locally opted-in, non-root terminal is retained. The separately owner-authorized fixed-target SSH console remains optional and isolated from agent authority. External Telegram/email notifications remain deferred. Keep signing private keys and production runtime state out of this repository and ordinary diagnostics.

## Layout

`cmd/monik-server`, `cmd/monik-agent`, `cmd/monik-service-host` and `cmd/monik-release` build the four programs. `internal/` contains protocols, storage, checks and lifecycle modules. `web/` contains UI source. `tests/browser/` and `tests/fixtures/` provide the synthetic browser scenario.

License: Apache-2.0; see LICENSE.

## V18 optional agent terminal

The Console page now offers a separate outbound agent/WSS transport alongside
explicit pinned SSH. Linux/systemd support is opt-in at local installation;
normal monitoring updates do not enable a shell. The agent terminal runs as a
separate unprivileged `monik-console` user, not root or the monitoring account.
Administrative tasks continue to use Direct SSH. See
[`V18_AGENT_CONSOLE_AND_TV_RU.md`](docs/V18_AGENT_CONSOLE_AND_TV_RU.md) and
[`SECURITY_ACCEPTANCE_V18.md`](docs/SECURITY_ACCEPTANCE_V18.md) for deployment,
revocation, sandbox limits and the mandatory native pilot. TV width adjustment
has an OK/arrow mode and native +/- buttons, independent of long-press dragging.
