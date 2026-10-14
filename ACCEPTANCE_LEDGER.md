# V22 Spark acceptance ledger (2026-10-15)

Base `dc6511eeeb2df9b85b5be59a1b6074648f0cdc08`. [Operator guide](docs/V22_SPARK_LIGHT_RU.md); [sanitized native evidence](docs/audit/validation-v22-spark-2026-10-15.json). Source, deployment, runtime and engineering targets have separate dispositions.

## Frozen build and installed artifacts

Code commit `b298673014e692fb3e897f5400aa45e6d5dc3322` was verified byte-for-byte against all 651 inputs of the paired build. Final acceptance documentation follows that source commit; it does not change the installed binaries.

| Artifact | SHA-256 / identifier |
|---|---|
| Build source | `517cb70f62b92ede4dc936fe6295d50d67a9a34d1800d4a1e63407222725f39f` |
| Linux AMD64 controller | `56af7ec0a8cd4e5e090770195dff02fbcff606dbe9e6ec5802fe3c6ea09ef939` |
| Linux ARM64 Spark light | `087c714caf6d9a780abf319b6df19895c765c0e40093f14ff827f8479b4227e3` |
| Existing `modelctl.py` | `856f7a8099abbdcf47bba1b1bb77366b8bb72c211535a63a12887872bfa70856` |
| Existing `legacy_runtime.py` | `492f2e50e8496efbd43abbfa59d010375b4a6face213c68056d6948728989f4d` |

The source/install manifest and launcher fixtures have distinct roles in [the controller package](tools/spark-controller/README.md). Fixtures are not deployment inputs. All three matched installer templates are served by the installed controller. Native unit ownership/modes, local policy/source pins and preserved trust were verified separately.

## Final checks

| Gate | Result and scope |
|---|---|
| Whole source/build | **PASS** Go race/vet/module verification; UI 215 tests/types/production build; AMD64/ARM64 and all three installer packages. Cross-build is not native boot. |
| Python and coupling | **PASS_OFFLINE** 342 copied Python controls; ten actual Python-produced fixture frames through strict Go consumer. Host effects in Python tests are injected at the adapter seam. |
| Independent reviews | **PASS** final coordinator/legacy/log-retention/codec and changed-area metadata/batch fixes. No weakened effect or replay checks. Deployment namespace/durability findings were fixed before F5 execution. |
| Publication | **PASS_SOURCE** 651 exact frozen inputs, 19/19 install hashes, 5/5 source copies; 637 previously audited unchanged hashes reused, 12 changed + 2 added reviewed. Real credentials/private state absent. |
| Local integrity preparation | **PASS** seven normal seals/rechecks. Five V4 bundles fully SHA-256 checked over **384181506686 bytes**; two legacy profiles use their original protected source/image/config qualification, not full legacy weight hashing. 496.305 s; unrelated state, collection lock and Ling identity unchanged. |
| Installed upgrade | **PASS bounded deployment** matched controller/templates + two monitor ELFs, three existing enrollment/trust identities and keys/settings/history/locks preserved. Model args/budgets/scripts/TUI retained. Earlier guarded partial F4 attempts and their exact continuations remain immutable private evidence. |
| Seven native observations | **PASS** one Ling READY on the same pre-existing instance, six stopped, all seven quality=ok. Original five-profile manifest unchanged; one Coordinator/StateStore/exclusion lock. |
| Native safe actions | **PASS** already-running Ling start no-op preserves instance/generation; inactive legacy Qwen/GPT stop no-ops return shutdown proof. Final controller status completed / each target succeeded. No real engine launch/stop claimed. |
| Native fixed logs | **PASS quiet tail/live** Ling fixed source, stale epoch refused409, bounded200 retained records, ~65s stream covered the under-log measurement, sampled content lacks the real local API key. Source and synthetic controls establish redaction behavior; no real secret was seeded in owner logs. Stream released. |
| Served browser | **PASS rendering** 15 screenshots desktop/mobile/TV viewport, final served artifact, no uncaught errors/overflow/unexpected mutation. Owner controls render. Viewer denial has source/earlier canary evidence; no new user account created. Physical TV NOT_RUN. |
| Real seven-engine lifecycle | **NOT_RUN** actual starts/stops/restarts/cancellation/cold start/full-context inference require a maintenance observation preserving owner intent. No extrapolation from fixtures or passive readiness. |
| Other gates | **NOT_RUN** synthetic flood/multi-viewer resource stress, physical TV, fleet/soak, perimeter and complete disaster restore. This is not a whole-product release declaration. |

## Actual whole incremental resources

Each final passive run lasted ~30 seconds. Product cgroup CPU includes spawned helpers/reaped descendants and is normalized to one core. PSS/RSS use paired process/child samples; disappearing children make peaks lower bounds. Charged cgroup memory lifetime peaks are a separate reported measure. Shared dockerd cost outside these cgroups is UNKNOWN.

| Run | CPU one core | Mean / sampled peak PSS | Sampled peak RSS | Duplex L2 lower bound |
|---|---:|---:|---:|---:|
| First F4, before batch | 49.20% | 52.49 / 84.08 MiB | 94.06 MiB | 2377 B/s |
| Final batch idle | 23.64% | 51.26 / 83.26 MiB | 95.94 MiB | 2379 B/s |
| Final batch, one quiet log view | 24.29% | 52.55 / 84.37 MiB | 98.55 MiB | 2589 B/s |

**Engineering targets MISSED**: <=32 MiB steady PSS, <64 MiB bounded-log PSS, <0.5% of one core, <2 KiB/s background wire. They are aims, not represented as passed release gates. Batch reduces nominal Bind+Snapshot helpers 8→2 and measured CPU by **51.95%**; all fresh physical checks remain. Final broker idle is approximately 1.63% main +21.03% reaped children; fresh systemctl/Docker/Python and collector nvidia/proc probes remain the measured cost. No cross-round effect/authority cache was introduced to improve the number.

Packet capture retained headers only, matched actual collector HTTPS/control connections and recorded zero kernel drops. It includes HTTP/TLS/control/ACK traffic. L2 excludes FCS/preamble/inter-frame gaps, so this is a conservative lower bound, not full physical wire. The quiet log view is not a flood test.

## Earlier F3 evidence, separate artifact

F3 passed isolated native ARM64 install/convert/reinstall/rollback with preserved identity, 18 CPU-only native canary controls and 15 served-browser screenshots. Those establish the stated fixture behaviors, not final seven-engine operation. Old resources: CPU1.144% / PSS30.7MiB, Spark JSON alone3474B/s. F3 lacked final seven-backend native work and cannot be compared as identical workload. Current native outcomes above are recorded on the installed batch build.

Private databases/backups, keys, raw owner logs, enrollment profiles, identifiers and handoffs are not published. Obsolete private fixture/reverse SSH and old canaries were closed; intentionally running monitors and Ling were preserved. Historical V21 gates below remain open where not genuinely exercised.

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
