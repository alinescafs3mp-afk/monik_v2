# Existing Spark controller integration

The [2026-10-15 source checkpoint](SOURCE-STATUS-2026-10-15.md) links the Nemo
metadata/Unicode repair, final LAN model guard, GPT fixed declarations and
Mistral compiler-cache correction, with explicit native and acceptance limits.

This directory contains the existing Spark V4 controller, its Monik typed bridge,
and the fixed Qwen/GPT adapters. They share the original StateStore and collection
lock. Monik does not introduce a second inference or memory reservation owner.

`python/` is executable source with offline tests. `copied-source/` contains only
the original non-secret manifest and two launcher fixtures used to verify
preserved engine arguments. Those launcher fixtures are never deployment inputs.
`deployment/` and `INSTALL-MANIFEST.json` are reviewed installation inputs, not an
automatic installer. They require comparison against the current host, protected
backups, preserved locks, loaded-unit verification and local policy approval.
The Qwen base unit and effective `Restart=no` override remain unchanged. The
existing model scripts and TUI retain their names; fixed delegates use the same
controller. No model download, launch, stop or credential change follows merely
from copying this directory.

Run the offline controls from `python/`:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -v
```

Docker, systemd, readiness and process observations in these controls are injected
at the host adapter seam. Transactions, state files, locking, receipt validation,
deduplication and cancellation execute the real controller. Passing these tests
does not establish native engine acceptance. `ROOT-RESOURCE-DEFAULTS.json` is a
sanitized explicit resource-shape fixture from an inert, never-started container;
it is not proof about any historical model container.

The broker accepts only fixed profile IDs and typed operations. It pins the
interpreter, project source, manifest, wrapper and unit definitions. Browser
requests cannot select paths, argv, environments, Docker names or adoption.
Readiness uses passive health/model metadata requests. The client retains control
of generation budgets.

Logs use fixed per-profile engine/action files, bounded rotation and local secret
redaction. The key values file is generated only on the host, is root-private and
must never enter this repository. Unknown ownership, ambiguous deletion and
incomplete physical release remain explicit failures. Durable unresolved receipts
are retained; retirement of verified completed records preserves replay guards.

During a cancelled Qwen shutdown, Docker's running snapshot can race the exit of
the captured engine PID. The observer still rejects that snapshot. Only positive
absence of that exact PID on the same cancelled STOPPING run permits the CLI to
attempt one fresh, complete release proof under the unchanged state lock. It
never repeats stop or kill. Reused PIDs, replaced identities, active resources or
an incomplete fresh proof retain the failure and reservation. The focused
`test_qwen_stop_exit_race.py` controls exercise this path with the real controller
and release checks; actual host stop/restart acceptance is a separate gate.
