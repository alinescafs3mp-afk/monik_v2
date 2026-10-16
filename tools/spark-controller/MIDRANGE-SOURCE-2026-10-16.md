# Three additional fixed profiles: source proposal

The controller accepts the original seven profiles plus fixed subsets of these
three. The deployment catalog includes all ten; each new row is **prepared=false**.
This checkpoint publishes source and offline controls. It contains no host policy,
approval, seal, credentials, weight files, binaries or runtime acceptance receipt.
Do not install this catalog over a live host manifest or enable a row by hand.

| Profile | Engine / weight format | Loopback port | Configured context |
| --- | --- | --- | --- |
| GLM4.5 Air Derestricted | llama.cpp CUDA / MXFP4_MOE | 31006 | 131072 |
| Gemma4 31B DECKARD HERETIC + DFlash | vLLM / NVFP4_AWQ | 31007 | 131072 |
| Nemotron Super49B v1.5 Uncensored | llama.cpp CUDA / Q5_K_M | 31008 | 131072 |

`midrange/PROVENANCE.json` identifies upstream artifacts. Disabled rows contain
expected file sizes/SHA256, exact argv, memory reservations and readonly mounts.
These are integrity requirements, not proof of a particular host's files. The
public deployment catalog retains the exact canonical original-seven E source
rows. Their hashes are checked in `midrange/SOURCE-PINS.json`. Earlier fixtures in
`copied-source/` deliberately remain unchanged for regression testing.

The seven-row baseline includes the prior Nemo metadata/Unicode fix with
ubatch1024/cache16384, preserved Qwen/Ling declarations, GPT's fixed streaming
source mount and MoE-only eager selection. Mistral uses diagnostic profile E:
native `flashinfer_cutlass` NVFP4 W4A4 MoE, FP16 KV8GiB, existing private compiler
cache and no Marlin override. Earlier C/FP8 profiles remain historical examples.
Profile preparation and successful inference quality are separate facts; this
source proposal makes no new correctness or performance claim about those rows.

## Gemma source boundary

The pinned ARM64 image is
`ghcr.io/aeon-7/gemma-4-31b-uncensored-nvfp4-dflash@sha256:6986488e0bb80d1a879fc6d40a66c048efd5c77c9ed897a68fe6a98dd88118f5`.
Its observed Docker image identity is that manifest digest. The raw OCI config
descriptor is a different object; both are recorded in the source inventory.
The direct command is `/usr/local/bin/vllm serve /models/deckard`; the image Bash
entrypoint is bypassed. Installed Python files live in **site-packages**.

Only this exact Gemma ID gains five fixed compatibility env values and two exact
readonly regular-file overrides. Controller validation binds both override
paths, targets, byte counts and SHA256 values; the ordinary protected-file,
owner/mode, symlink and stable-stat checks still apply. Other profiles retain
their existing environment and directory-mount rules. The supervisor adds the
same private 2GiB executable compiler tmpfs already used for Mistral, with cache
env paths pointing into it. Generic `/tmp` remains separate and bounded.

The paired parser/serving correction handles a tool name, arguments and delimiter
arriving in one update, several tool calls in one update, and terminal catchup
without losing earlier calls, content or reasoning. `gemma4-source-fix/` preserves
the upstream Apache notices and source-only regression fixtures. The controls
reproduce the original loss, then check split/coalesced, Unicode, multi-call and
terminal paths. They execute extracted methods with protocol-shaped records;
actual vLLM imports, model output and GPU behavior remain untested here.

`gemma4-source-fix/SOURCE-INVENTORY.json` binds the 32 image source files used for
static dispatch inspection. The selected `Gemma4ForConditionalGeneration`
registry entry invokes the multimodal wrapper, which constructs the registered
`Gemma4ForCausalLM` text model while preserving its nonnull quant config. The
quantization override/factory selects `modelopt_fp4` and its NVFP4 linear method;
the weight mapper preserves text down/o scale names. The safetensors default
loader calls Base postload, generic module postload and the selected ModelOpt
method. That method replaces FP8 scale NaN bytes `0x7f/0xff` with zero before
kernel preparation. This is a **numerical transformation** of loaded tensors.
Static reachability does not establish an actual scrub count, remaining finite
scales, numerical quality or physical capacity. No persisted derivative is
published or required by this source checkpoint; originals must remain immutable.

At 131072/B16, the DFlash allocator's five shared plus five isolated pools yield
a conservative 42213048320-byte cache estimate including prefill/padding. The
simpler generic five-pool startup estimate can undercount this layout. The row
reserves 40GiB cache, 12GiB workspace, 8GiB loading peak and 8GiB headroom in addition
to target/draft weights. The future `.65` condition requires at least
120954546314 bytes of **actual CUDA/vLLM-visible total memory**, plus measured
available memory of at least 96536948928 bytes and measured allocation/workspace
checks. Linux MemTotal cannot satisfy that CUDA predicate. Engine fit, usable
blocks and 131072-token operation remain unqualified; advertised 262144 is not
the configured or accepted window.

Main output8192 plus history/AUX2048 leaves at most120832 input tokens. Gemma's
native template thinking is retained; no unsupported generic effort or thinking
OFF override is added. The sampler candidate is temperature1/top_p.95/top_k64.
The two llama profiles retain their own samplers and single-slot server settings.
Nemotron49's system `/no_think` support does not make ignored CTK false a qualified
Hermes title-OFF integration. Title/AUX and full compressor qualification remain
separate live gates.

## Operational assets and reproduction

Three template units, nine home delegates, a fixed sudoers fragment and disabled
TUI/catalog additions are under `deployment/`. Launcher declarations contain no
claim that files were created. Existing home names and aliases require fresh CAS
comparison; preserve owner edits and the original collection lock. The pure
`midrange/policy_draft.py` appends fixed source bindings with empty allowed actions
for disabled rows; it does not approve or install a policy. Ordinary local worker
approval/verification and all ten full native prepares must happen on the actual
protected release. A source digest or typed boolean cannot replace those steps.

Run local source controls and rebuild the two static ARM64 programs from this
repository with Go1.27.2:

```sh
(cd tools/spark-controller/python && python3 -B -m unittest discover -v)
(cd tools/spark-controller/gemma4-source-fix && python3 -B test_parser_offline.py)
go test -race -count=1 ./internal/spark ./cmd/monik-spark-agent
(cd tools/spark-controller/lan-api && go test -race -count=1 \
  lan_proxy.go lan_proxy_test.go ling_model_test.go nemo_model_test.go \
  midrange_test.go gemma_count_test.go)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=readonly -buildvcs=false -trimpath \
  -ldflags='-s -w -buildid=' -o /tmp/monik-spark-agent ./cmd/monik-spark-agent
(cd tools/spark-controller/lan-api && GO111MODULE=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags='-s -w -buildid=' \
  -o /tmp/spark-v4-lan-api lan_proxy.go)
```

Use separately reviewed root staging, safe parents, backups, atomic replacement,
source/config/policy CAS, the existing collection lock, ordinary native approval
and physical-release checks. Never copy seals from examples. A partial publish,
changed home alias/config, unknown release or cleanup must remain PARTIAL/UNKNOWN;
do not repeat a possibly executed operation. The private full installer and
host-specific independent evidence are deliberately excluded from this public
source tree. This directory is not an unattended live installer.

The LAN count alias is a **separate, not-installed source change** after the
frozen ten-profile proposal: authenticated exact-model POST `/v1/tokenize` on
31007 forwards to `/tokenize`. Query and full bytes including tools/CTK are
preserved. Model validation and the existing shared64MiB body budget apply before
upstream. Other seven ports/routes retain their behavior. No count/inference
rendering equivalence, live endpoint availability, 120k LONG, sustained load,
queue admission or end-to-end model acceptance follows from these offline tests.
