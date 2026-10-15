# Exact GPT precision and cache controls

`legacy_runtime.approved_gpt_declarations()` returns seven complete local rows for
`gpt-oss-120b-existing-configured`. `definitions()` stays the historical reference.
These are accepted declarations for controlled qualification, not evidence of
runtime correctness, speed, cache reuse or model acceptance.

| Index | MXFP4 layers | FP8 KV scales | Persistent cache control |
| --- | --- | --- | --- |
| 0 | `moe,qkv,o,lm_head` | Existing default | Existing default |
| 1 | `moe` | Existing default | Existing default |
| 2 | `moe,qkv,o,lm_head` | `--calculate-kv-scales` | Existing default |
| 3 | `moe` | `--calculate-kv-scales` | Existing default |
| 4 | `moe` | Existing default | Exact XDG path below |
| 5 | `moe` | `--calculate-kv-scales` | Exact XDG path below |
| 6 | `moe` | Existing default | Exact XDG path below, plus fixed Harmony stream helper |

Rows 4 and 5 add only
`XDG_CACHE_HOME=/srv/spark/hf-home/gptoss-runtime-cache`. The HF_HOME bind mount is
already read-write; no new mount or general environment override is allowed.
The pinned vLLM source resolves its default `VLLM_CACHE_ROOT` to
`XDG_CACHE_HOME/vllm`. This does not establish every TorchInductor, Triton or
FlashInfer cache path; their environment controls are not added. CUDA graphs
still require capture in the new process. Compare row 1 with 4 or row 3 with 5
to isolate the cache declaration from precision changes.

Row 6 preserves row 4 and adds one fixed read-only source mount at
`/usr/local/lib/spark-gptoss/serving_chat_stream_harmony.py`, whose SHA-256 is
`443bcfc4b8f98d03072b01d6edd72cfec50ec81fd1973c9d2cab9272d5815616`.
When a streaming parser step discovers a new tool recipient and already contains
argument text, the original helper discarded that text. The replacement keeps
`delta_text` in the initial tool delta. It changes no model weights, sampler or
token budget. This source correction does not establish that all malformed
Harmony headers, repeated final text or task failures are resolved. Fresh native
qualification is required. See [the correction notes](NATIVE-CORRECTIONS-2026-10-16.md).

The exact pinned image recognizes `--mxfp4-layers moe`: only checkpoint experts
retain MXFP4, while dense attention projections and the head remain BF16.
`--calculate-kv-scales` requests the installed FP8 scale calculation, which runs
once and disables itself afterwards; record which initial forward sets the
scales. Neither control is a demonstrated cure for malformed tools or false
file claims. All rows preserve the image and source pins, explicit CUTLASS,
FLASHINFER attention, FP8 KV, context 131072, max sequences 8, batch tokens 8192,
tool/reasoning parsers, memory admission, ownership, locks and stop handling.
Qwen and other model declarations are unaffected.

The integrator selects a row through the existing reviewed installation process:

1. Independently review the exact source, focused tests and pinned-image source
   evidence. Keep protected original source and manifest copies with their hashes.
2. Confirm the tracked GPT instance is stopped and its ordinary release proof is
   complete. Preserve the controller's existing collection lock and sole writer.
3. Install the reviewed `legacy_runtime.py` and refresh its installation-manifest
   hash. In a protected staged copy of the existing seven-row controller manifest,
   replace only the GPT row with the complete selected object from
   `approved_gpt_declarations()[INDEX]`; validate that manifest through the existing
   `modelctl.validate_manifest`. Do not construct flags or environment overrides.
4. Apply the protected manifest and perform the normal local policy reapproval
   against its new full profile revision. Run the existing
   `spark-modelctl-v4 prepare-legacy gpt-oss-120b-existing-configured` for its full
   integrity seal. Every different row requires a fresh seal; previous approvals
   and runtime observations do not transfer.
5. Use only the existing tracked `spark-modelctl-v4 start`/`stop` route and ordinary
   identity/release checks. Record exact row and source hashes with actual native
   quality, scale, cache-artifact and cold/warm observations before acceptance.

Rollback follows the same tracked stop/release process. Restore the protected
prior manifest and source bytes if reverting the source package, then repeat
normal policy reapproval and full integrity preparation. Row 0 restores the
historical GPT declaration while keeping the reviewed source package. Preserve
diagnostic evidence and caches; rollback does not authorize broad cache deletion.

Offline controls (from `python/`):

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -v test_gpt_approved_declarations test_qwen_approved_declarations test_legacy_shared test_launch_identity_regression
```

The controls validate the real fixed-row, manifest, container-argv and seal
boundaries with host effects absent/injected. They do not run a model.
