# V22 Spark acceptance ledger (2026-10-15)

Base `dc6511eeeb2df9b85b5be59a1b6074648f0cdc08`. This ledger separates source, installed deployment and native/runtime acceptance. [Operator guide](docs/V22_SPARK_LIGHT_RU.md). A PASS applies to the stated artifact and scope; earlier candidate results do not transfer to changed executables or backend code.

## Artifact boundaries

Go/UI release `release-final7-20261015T0012` has the following identifiers. Its build source ID covers that build's Go/UI inputs; later Python backend corrections are pinned separately and are not attributed to that build source ID.

| Artifact | SHA-256 / source identifier |
|---|---|
| Go/UI build source ID | `29cdcdb471291d4fa4ac714dd622b758034110d547d9ca17d9585aef5ec51abd` |
| Linux AMD64 controller ELF | `56458f88b7465e0c6963ddd0b085db0f48881099d476347c0181e880a972429d` |
| Linux ARM64 Spark light ELF | `d854f0b4050b41c5c1901d3ca4eda149489a44735b2aa3df3812a62bda378bc6` |
| Later `modelctl.py` backend | `856f7a8099abbdcf47bba1b1bb77366b8bb72c211535a63a12887872bfa70856` |
| Later `legacy_runtime.py` backend | `492f2e50e8496efbd43abbfa59d010375b4a6face213c68056d6948728989f4d` |

The installation inputs and original launcher fixtures have distinct roles in [the controller source package](tools/spark-controller/README.md). Fixture launchers are not deployment inputs. Exact installed ownership/modes, loaded-unit qualification, policy/source pins and native outcomes need their own evidence below.

## Current evidence

| Gate | Result and actual scope |
|---|---|
| Go/UI source and builds | **PASS**: whole Go race, vet, module verification; UI 215 tests, type check and production build; all three installer templates; AMD64 and ARM64 builds. Cross-builds do not prove native boot or model operation. |
| Final copied Python controls | **PASS**: 342 tests in 14.141 s, including fixed systemd template metadata and strict typed D-Bus proof of omitted empty optional commands. Real Coordinator/StateStore transactions execute with host effects injected at the adapter seam. |
| Producer/consumer contract | **PASS_OFFLINE**: 10 actual Python-produced fixture frames accepted by strict Go consumer; scoped independent review PASS. They are generated fixtures, not ten native model observations. |
| Legacy/coordinator fixes | **PASS_OFFLINE**: strict captured declarations, source bootstrap, immutable re-adoption, explicit resource shape, typed cancellation/reaper authority and noarg legacy stop delegate. Later native metadata/empty-command source deltas also reviewed independently. Installed/runtime acceptance remains separate. |
| Logs/retention/codec fixes | **PASS_OFFLINE**: independent changed-scope review closes redaction pin-custody, malformed-receipt retention and declared-legacy composition findings. Actual reader/emission revalidation, bounded buffers and replay guards covered. Unchanged bounded authenticated gzip review reused on the same codec bytes. |
| Source publication hygiene | **PASS_SOURCE**: no real credentials/private state found in the reviewed public source/package; portable offline bundle and installation manifests checked. Subsequent source changes still require scoped publication checks. Private reports/caches are not publication inputs. |
| Deployment checkpoint | **DEPLOYED**: F4 controller and both Spark monitors running; three existing enrollment/trust identities retained. Seven profiles route through one pre-existing Coordinator; original five-profile V4 manifest and script/TUI interfaces preserved. This row is not final runtime acceptance. |
| Runtime preservation checkpoint | **OBSERVED**: Ling READY on the same previous instance; Qwen owner STOP and GPT/MiniMax stop intent preserved. No model generation/cold start performed during the Monik upgrade. No claim that all seven engines ran. |
| F4 installed/backend native acceptance | **PASS bounded native observation/control slice**: seven real profiles report `quality=ok`, Ling READY and six stopped; same three enrollment/trust identities retained. Existing Ling start was a no-op at the same generation; stopped legacy Qwen/GPT returned completed/succeeded with shutdown proof. Fixed Ling log SSE returned ten records with the local API key absent; stale log epoch was refused. No real cold-start, running-engine stop or restart acceptance is claimed. |
| F4 served browser | **PASS rendering**: final served F4 bundle, 15 screenshots at desktop/mobile/TV viewports, no browser errors, horizontal overflow or unexpected requests. Owner model controls were rendered. Source tests cover viewer denial; this browser run did not create a viewer account. Physical-TV hardware remains separate. |
| F4 incremental footprint | **MEASURED, targets missed**: idle 30.018 s, collector 0.858% + broker 48.346% of one core, PSS mean 52.49 MiB / sampled peak 84.08 MiB, RSS sampled peak 94.06 MiB. Transient child races make memory peaks lower bounds; shared dockerd CPU unknown. Actual duplex TCP L3 2331 B/s, L2 lower bound 2377 B/s, packet drops zero. Repeated sealed Python/single-profile probes are the confirmed main cost; a Go batching correction is under validation. No active-log stress or model generation in this run. |
| Real seven-engine lifecycle | **NOT_RUN**: maintenance-owner execution required for actual start/stop/restart, live cancellation/release/no-victim, full-context concurrency, inference and cold-start measurements. No extrapolation from injected adapters or Ling passive readiness. |
| F4 log stress / inference cold start | **NOT_RUN** unless an actual bounded observation is added. Offline stream bounds and redaction controls do not establish stress resource behavior. |

Engineering targets: steady PSS <=32 MiB; bounded-log PSS <64 MiB; CPU <0.5% of one core; background wire traffic <2 KiB/s. CPU is a target rather than an automatic release blocker. Record missed targets and the measured scope honestly.

## Historical F3 evidence — does not close F4

| Gate | Earlier actual result and limit |
|---|---|
| Native installation/recovery | **PASS** in isolated ARM64 fixture: install, preserved-identity full-to-light conversion/reinstall and rollback. Fixture trust is distinct from production enrollment. |
| Native lifecycle | **PASS**, 18 CPU-only native canary controls. Actual dispatch/reconciliation, staged restart, safety stop, revocation and bounded logs; no model/GPU workload. |
| Browser | **PASS**, 15 screens. Earlier served artifact; neither F4 seven-backend acceptance nor physical-TV proof. |
| Steady resources | CPU **1.144%** of one core, above the **0.5%** target; PSS **30.7 MiB**, within **32 MiB**. Earlier sample does not establish F4 footprint or log stress. |
| Traffic | Spark JSON alone **3474 B/s**, above **2 KiB/s**; this is not total wire traffic and does not include later gzip behavior. F4 wire measurement is pending. |

Final dispositions use PASS/FAIL/BLOCKED/NOT_RUN/PENDING with explicit scope and reasons. Production database/state, credentials, raw owner logs, enrollment profiles, private machine IDs/paths and personal handoff artifacts are excluded from publication. Historical V21 limits below remain open where not genuinely exercised.

## Historical V21 acceptance ledger (2026-09-16)

Reviewed base `3303e991a2624c945662355e22fa7cfb8ef06a6a`, exact tree `f319e9b12f6e4091a95dea162046f1e509fe2078`. Patch applied to exact auditor tree `12d287abd20118dbae11c225cb759f611bca0f38`. Previous V20 host ledger is preserved in `docs/history/ACCEPTANCE_LEDGER_THROUGH_V20.md`; it is NOT the V21 browser acceptance. Auditor browser was BLOCKED_BEFORE_LOGIN; host executed that gate. See `docs/audit/validation-review21-2026-09-16.json`.

| Gate | Actual host evidence |
|---|---|
| Go 1.27.1 race | 478 top-level PASS, 714 test/subtest/seed PASS, 27 tested packages. Opt-in/helper skips in the ordinary suite; native process cases run separately. |
| Frontend | 199 Node tests PASS; tsc and production Vite build PASS. Includes true composable/SFC execution plus one extra omitted-Boolean header case; not vue-tsc or pixel geometry. |
| Repeats | New V21 server/storage tests 10 shuffled race passes; 47 UI tests repeated 10 times. |
| Reproductions | Five baseline groups, nine failing leaf cases, corrected final tests PASS. |
| Static/dependencies | go vet and go mod verify PASS. Lockfiles unchanged. Not a CVE audit. |
| Build | Four binaries each Linux/Windows amd64; credential-free installer templates linux-amd64/arm64 rebuilt after commit. Windows is cross-compilation only. |
| Native processes | PASS 3/3 uid=1000, including signed replacement and failed-candidate rollback. Not systemd/boot. |
| Console UID fixture | NOT RUN (needs disposable root). `TestV18NativeActivatedPeerUIDAndKeyIsolation` skipped. |
| systemd unit syntax | PASS with `MONIK_SYSTEMD_VERIFY=1`. Does not install or start any service. |
| Browser | Host 50/50. Includes five new clean-TV scenarios plus prior V20/V19/V18/V17/V16 cases. Fixture loopback only. CSP `script-src 'self'` was not weakened. Physical TV/Yandex NOT RUN. |
| Full release | NOT ACCEPTED: restore, independent service-host recovery, boot, external perimeter and soak gates remain open. |

Host extras versus auditor tree `12d287abd20118dbae11c225cb759f611bca0f38`: this ledger, `IMPLEMENTATION_STATUS.md`, `docs/audit/validation-review21-2026-09-16.json`, Vue Boolean default so ordinary overview resize handles stay visible, CSP-safe browser waits, and restoring `monik:display=auto` after the clean-TV fixture. No locator, origin, CSRF, TLS or geometry check was weakened. Existing agents need no binary replacement or re-enrollment. Deploy matched server/UI, and rebuild matching installer templates for later downloads. Hiding TV controls is a local presentation choice, NOT a restricted credential or shell-policy change.
