# Native synthetic Spark control canary

This package is prepared for the native-run owner. Cross-builds and offline
refusals are preparation evidence; no native execution is claimed here. It does
not enroll an agent, load weights, invoke inference, use a GPU, adopt Qwen, or
modify a real model/root launcher. Product source is unchanged by this package.

The fixed existing isolated light enrollment is
`c7966ecd-e521-4e18-9142-dff2bc9d218e` in
`/var/lib/monik-spark-native-canary-20261014/agent.json`, owned by `monik-canary`.
The controller is the actual fixture at `https://127.0.0.1:18786`, with its pinned
fixture CA. Controller credentials and cookies remain in memory. Output contains
public binding/receipt evidence, hashes and synthetic redaction observations;
it never saves raw log lines or authentication material.

## Native staging, once

The owner first reserves the native exclusive slot and confirms the standard
`/run/monik-spark/control.sock` is absent. Maintain that exclusivity through run
and cleanup: the harness remains separate from any concurrent production broker
start. The final product additionally locks the socket path, rejects active or
uncertain peers and cleans only its verified own socket inode. No production or parallel broker start is authorized by this package.

Stage root-owned regular files, mode 0755, under the root-protected directory
`/opt/monik-spark-native-control-canary-20261014`:

- `spark-control-canary`: the cross-built native ARM64 helper.
- `fake-engine`: the already-built CPU-only fake engine from the native-resume kit.
- `monik-spark-agent`: the parent's final native product build.
- `CANARY_ONLY`: mode 0644, containing exactly `MONIK_NATIVE_CONTROL_CANARY_20261014`.

The parent must verify uploaded SHA256 values against the local build proof.
The helper requires the isolated enrollment to already be installed as
`spark-light`; it validates the existing collector UID/GID and controller ID.
It accepts no path, command, arbitrary unit, model or identity overrides.

`native_stage.py plan` prints the exact four unit definitions without effects.
After reviewing them, the native owner runs `native_stage.py apply` as root on
Linux ARM64. It refuses occupied destinations, loaded/transient units, observed
or prospective systemd drop-ins, and occupied loopback ports 18971–18973. It
creates only its own root-protected policy/state and three NONROOT synthetic
services plus its root typed broker. Services have 128 MiB/0.5 CPU/16 task
limits; the broker has 256 MiB/1 CPU/64 tasks. No service is enabled for boot.
It creates 4096 empty `.bin` metadata fixtures (no model content), then invokes
the actual local `approve --draft ... --output ... --enable` and `verify-policy`
CLI. Approval derives the effective unit hashes and profile revisions; the
draft deliberately contains no invented binding hashes. Only the broker starts.

The policy lives at `/etc/monik-spark-native-control-canary-20261014/policy.json`;
the journal lives at `/var/lib/monik-spark-native-control-canary-20261014`.
Wait for the existing light collector to bind during its next ordinary sample,
then execute `spark-control-canary verify` once. A blocked/failed staging phase
is not retried in place: preserve its partial paths and inspect the cause.

## Coherent test sequence

Run these native commands, capturing each JSON and exit code separately:

```text
python3 local_cli_cycle.py
spark-control-canary stale-bindings
spark-control-canary preflight-cancel
```

`local_cli_cycle.py` invokes the actual public product CLI for a real
start/replay/restart/stop cycle, requiring new native generation and passive
readiness. It has a 75-second test budget and an independent 25-second cleanup
budget. `stale-bindings` uses the root broker's typed request interface and pins
the actual expected rejection codes for generation, revision, epoch, session
and dispatch lease; it also checks generation did not change.

`preflight-cancel` performs **one** native start attempt with the real metadata
scanner and observes the durable journal. PASS requires observing
`preflight_pending`, a distinct successful stop receipt, and
`start_cancelled_by_stop` rejection. The window is timing dependent: if those
conditions are not proved, it returns `test_status=NOT_OBSERVED` and stops only
its own preflight service. `success=true` then means cleanup/preparation worked,
not that preflight cancellation coverage passed. Its attempt is bounded to
45 seconds plus an independent 15-second cleanup budget; there are no retries.

On the controller host, the owner runs:

```sh
python3 tests/native/spark_control_canary/controller_run.py --execute --time-budget 240 --output /home/jericho/.cache/sol-monik-spark-v2-20261014/native-control-harness/native-api-control.json
```

The API run requires exactly the three synthetic profiles. It checks real
start/stop and staged restart, running/readiness generation changes, idempotent
replay and conflicting replay denial, typed-field rejection, stale bindings,
actual journal secret/token/ANSI redaction, generation revocation and logout
revocation of live log streams. The agent lease/batch paths may close either
stream with `authority_or_source_changed`; generation change plus stale binding
denial and logout plus denied stream access prove the respective causes.
A 20-second nonroot `ExecStartPre` creates the
real `activating` window for a safety stop of a linked restart start. The parent
restart and the separate stop must have distinct truthful receipts.

It also runs standalone start → distinct stop, preserves the original start
receipt, requiring `failed/start_cancelled_after_stop` for this exact observed
positive. An unconfirmed `unknown_result` fails this cancellation proof;
requires physically verified shutdown and a full measured 25-second no-resurrection
window, replays the original request, and demands a **fresh** start be admitted
after the verified stop. A lingering `model_operation_in_progress` reservation
is recorded as a product FAIL. It is never hidden by substituting only a linked
restart test. Main execution is bounded by the requested budget; cleanup has
its own 50-second budget. Cleanup checks any possibly lost start dispatch
through its maximum lease window and reports unconfirmed cleanup honestly.

Native systemd cancellation can retain `failed/failed` after its real
`ExecStartPre` is killed. The failure stays visible. This state is accepted as
physical shutdown only when the adapter observed complete zero `MainPID` and
`ControlPID`, an empty `Job`, and a cleared or verified empty cgroup. The stop
receipt must carry `shutdown_verified: true`, `not_applicable` readiness, the
typed `failed_service_shutdown_verified` reason and a current invocation
identity. The harness binds that receipt to fresh state, generation, profile
revision, control epoch and agent session throughout all 25-second windows.
An arbitrary `failed` label or an incomplete/stale/unbound receipt fails.

The result and append-only dispatch journal are reserved before login/effects,
with exclusive creation and mode 0600. Each request key and binding is flushed
before its POST, so a lost HTTP response cannot remove it from owned cleanup.
All final stream snapshots are checked for leakage and reader errors. Only
touched fixed profiles, owned streams and owned browser sessions are cleaned.

## Final native cleanup

`native_stage.py cleanup-plan` is read-only. It checks exact staged file bytes,
root protection, actual fragments and absence of drop-ins before listing the
four owned services/files. The native owner stops the three synthetic services
first, then `spark-native-control-broker-canary.service`, verifies inactive/no
processes and socket removal, and may delete only those four exact owned
fragments before `daemon-reload`. Retain enrolled identity/account, source
binaries, policy, journal and private evidence. Never stop/delete
`monik-agent.service` or any production model/container/launcher here. Stopping
the owned broker is also the final fence against pending canary effects after
an API failure; API success alone does not prove final native cleanup.

No result closes real inference, Qwen/root launcher control, boot acceptance,
or live telemetry for additional GPU/engine adapters. Those require their own
execution evidence. An observation-only root profile belongs to a separately
reviewed policy, not this exact three-profile synthetic effect boundary.
