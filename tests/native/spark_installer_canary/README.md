# Native Spark installation canary

Linux ARM64 only. This runner never enrolls an agent and has no path overrides.
The owner of the native run must first verify that `monik-agent.service` is absent,
then stage the already enrolled isolated fixture under the exact private state below.
No existing model unit, container or production identity belongs to this canary.
The runner also verifies resolved systemd absence before first installation, then
the exact fragment path with no drop-ins or transient unit before further phases.
It rejects prospective `.conf` drop-ins in `monik-agent.service.d`,
`monik-.service.d` and `service.d` across the manager's actual unit search paths,
including orphaned drop-ins that are not visible until daemon-reload.

Fixed paths:

- Root-owned, not group/other-writable marker: `/opt/monik-spark-native-canary-20261014/CANARY_ONLY` (regular file; content is immaterial).
- Root-protected source binaries: `/opt/monik-spark-native-canary-20261014/bin/{monik-agent,monik-service-host,monik-spark-agent}`.
- Prefix: `/opt/monik-spark-native-canary-20261014/prefix`.
- Private enrolled state: `/var/lib/monik-spark-native-canary-20261014`; `agent.json` must name this exact state and a canonical credential file directly inside it. Any update root must also be a canonical file directly inside it.
- Service: `/etc/systemd/system/monik-agent.service`, dedicated user `monik-canary`.

Run each phase once, capturing its JSON and separate native/identity evidence:

```text
spark-installer-canary install-full
spark-installer-canary convert-light
spark-installer-canary replace-light
spark-installer-canary rollback
spark-installer-canary cleanup-plan
```

`replace-light` deliberately reuses the unchanged light source and verifies the
original-full recovery digest. A distinct light upgrade would create a latest-light
recovery point and is a separate experiment. `rollback` invokes the public native
rollback API, including recovery after an interrupted conversion; it preserves
credentials and does not delete recovery evidence.
`cleanup-plan` only emits the plan for the exact checked canary unit and executes
no cleanup. Retain state, account, source binaries and recovery artifacts as evidence.

Build from the repository with Go 1.27.2:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 /home/jericho/.local/go/bin/go build -trimpath -o /home/jericho/.cache/sol-monik-spark-v2-20261014/native-install-harness/spark-installer-canary-linux-arm64 ./tests/native/spark_installer_canary
```

Cross-build and JSON API success do not close boot, process, identity or model
acceptance. The native-run owner records those checks separately.

For a distinct final candidate on this existing custom-prefix canary, reuse
`rollback` followed by `convert-light`. Validate all candidate hashes and staging
paths before stopping anything, retain the old source and recovery evidence, and
stage the new light bytes at the fixed `bin/monik-spark-agent` source between the
two phases. The rollback must first restore the managed Full unit and workers.
The conversion then calls `InstallSparkLinux` with the same fixed prefix, state,
config, unit and account, with `ConvertFlavor=true`; it does not re-enroll.
Do not invoke the product CLI's default-prefix installation or rollback for this
canary, and do not use `replace-light` for different bytes.

Claim a new private evidence file exclusively before the first effect, and retain
failure evidence without replaying the same attempt. Capture the original
`recovery.json` digest and part hashes before rollback. After conversion, expect
a fresh settled General recovery journal naming the final light candidate;
its ID and digest change, while the restored Full recovery part hashes must be
preserved. Record active and protected worker hashes, direct flavor, the
`monik-canary` UID, identity/credential continuity, and the unchanged real model
unit/process separately. If a separate isolated control broker is replaced,
verify and stop only its exact canary unit, preserve all synthetic units stopped,
and observe the new collector binding before claiming its verification succeeded.
