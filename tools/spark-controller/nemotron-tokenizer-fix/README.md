# Nemo tokenizer correction: reproducible source package

This package recreates the metadata and Unicode tokenizer correction validated on
ARM64 on 2026-10-15. It contains the exact metadata-copy helper and C++ patch,
an import-only GGML configuration, explicit build instructions and selected
sanitized evidence. It contains no weights, runtime binaries, private corpus,
credentials or deployment installer. Python modules perform no work on import.

The two corrections are required together:

* The original GGUF has 994 empty special-token spellings (IDs 0–11 and 18–999),
  `pixtral` preprocessing and incorrect automatic BOS insertion. The helper copies
  the file to a **new explicit destination**, restores the pinned spellings,
  selects `tekken` and sets add-BOS/add-EOS false. Ordered merges, token types,
  ordinary vocabulary, tensor descriptors and all other metadata stay unchanged.
* The old TEKKEN implementation does not implement the official regex/category
  behavior for combining marks and other Unicode boundaries. The exact patch
  changes two implementation files and adds two private headers. Its complete
  2,363-record classification table is embedded; no private generator is needed.
  The selector uses the exact pinned regex; public ABI headers remain unchanged.

See [PINS.json](PINS.json) for immutable upstream revisions, input/output hashes
and toolchain, [PROVENANCE.json](PROVENANCE.json) for source transformations,
and [BUILD.md](BUILD.md) for the isolated core-only build. Obtain the upstream
source and the two tokenizer JSON files at the pinned URLs before starting;
these tools fetch nothing. The existing ARM64 runtime is an explicit external
prerequisite, verified against [runtime-original.json](runtime-original.json).
This recipe is specific to those inputs and refuses mismatches.
Check package consistency with `sha256sum --check SHA256SUMS` from this directory.

## Recreate the metadata copy

These are documentation examples; package preparation did not execute them.
Set each path explicitly. The source is the original GGUF with SHA256
`870e367f1ae025cba5ad3709d9bbb05444d9b4a51e89ca162021736cefcdd0cf`.
The parent directory of the new destination must already exist. Do not select an
existing model, the shared Ling runtime or another active deployment as output.

```bash
NEMO_PACKAGE=/absolute/path/to/nemotron-tokenizer-fix
NEMO_ORIGINAL=/absolute/path/to/original.gguf
NEMO_TOKENIZER=/absolute/path/to/pinned/tokenizer.json
NEMO_TOKENIZER_CONFIG=/absolute/path/to/pinned/tokenizer_config.json
NEMO_NEW_MODEL=/absolute/path/to/new-output/nemo-metadata-fixed.gguf

python3 -I "$NEMO_PACKAGE/repair_gguf_metadata.py" plan \
  --source "$NEMO_ORIGINAL" --tokenizer "$NEMO_TOKENIZER" \
  --tokenizer-config "$NEMO_TOKENIZER_CONFIG"
```

`plan` reads the GGUF header and the supplied tokenizer files; it neither creates
a candidate nor validates the full source-file hash. Expected plan: 994 token
replacements, payload offset 7,906,880 → 7,919,776, shift 12,896 bytes, 763 tensor
descriptors unchanged. Review the plan before explicitly invoking the copy:

```bash
python3 -I "$NEMO_PACKAGE/repair_gguf_metadata.py" copy \
  --source "$NEMO_ORIGINAL" --destination "$NEMO_NEW_MODEL" \
  --tokenizer "$NEMO_TOKENIZER" --tokenizer-config "$NEMO_TOKENIZER_CONFIG" \
  --min-free-after-bytes 8589934592
```

The copy needs space for 68,578,330,784 bytes plus the selected reserve. It hashes
the entire original while copying, then rereads and hashes the entire new
candidate. The observed copy took 531 seconds; this is substantial disk I/O.
It uses a unique temporary file, verifies the original full SHA and unchanged
payload, and publishes atomically with a no-overwrite hard link. Failures remove
the owned partial file; source and pre-existing destination are preserved.
The command prints a JSON receipt; save it in a separate new job-owned log path.
The helper creates the candidate with mode 0600 and changes no ownership. The
original root wrapper separately applied deployment ownership/mode and enforced
1,536 MiB address space, 1,200 CPU seconds, an 80 GiB file-size cap and two-CPU
affinity. This reusable helper does not install that wrapper or change host
resource policy; use the execution environment's finite resource/wall limits.

Expected candidate SHA256:
`601aa97e15921593ece860c8d962cbba3c4499b0845211977eb9a4cbdb2f746f`.
Unchanged 68,570,411,008-byte payload SHA256:
`1971be0201ddf7a6591b3357f7de502d25e5b32eb1843da9050e72b5604a083f`.
Unexpected tokenizer/source hashes, nonempty token drift, reordered merges,
wrong token types, a different defect set, insufficient disk or an existing
destination cause failure. The public CLI provides no pin-bypass switch.

## Evidence and validation boundary

[SELECTED-EVIDENCE.json](SELECTED-EVIDENCE.json) records the actual native ARM64
vocabulary result: **145,820 rows, zero failures**, using both add-special modes:
24 conversation goldens, 1,000 special spellings, 130,072 ordinary decoded
vocabulary strings and 14,724 Unicode inputs. The old core still failed the
Arabic `اً`, Devanagari ` के` and Bengali `য়` controls with repaired metadata.
The new core with original metadata split ChatML tags and inserted an extra BOS.

The first complete native gate **failed** in the old-metadata negative-control
oracle: it expected native empty strings, while the engine exposes 994
`[EMPTY_i]` placeholders. That failure remains preserved. Only this negative
control was rerun after correcting the expectation; the unchanged full candidate
PASS and old-core controls were reused. The reported final result is composite,
not a second complete gate. Raw empty strings in the original GGUF remain a fact.

The qualification used `vocab_only=true`, a non-null empty devices array,
`n_gpu_layers=0`, `load_mode=NONE`, `no_alloc=true`, no backend initialization,
no context and no model evaluation. Loading the vocabulary read about 7.9 MB.
The full GGUF hash came from the separate copy/reread proof. The historical full
corpus is not distributed here; the small tests below do not reproduce or replace
that native acceptance. A new library/toolchain/model combination requires its
own native vocabulary and deployment acceptance.

```bash
python3 -I "$NEMO_PACKAGE/test_metadata_guards.py"
```

These bounded tests use newly generated tiny synthetic GGUF data only. They
check payload/source preservation, no overwrite, hash rejection/cleanup,
nonempty drift rejection, reread corruption detection and tokenizer-pin
rejection. They do not load an actual model, build, contact a network or start
a runtime.

## Deployment is separate

Building a new bundle and creating a corrected GGUF do not select either one.
Keep the original GGUF, source and shared Ling runtime intact. Record exact model
and core hashes, validate the newly built bundle's ABI and native vocabulary,
then explicitly configure **Nemo alone** to use the new model and new runtime
path through the deployment's normal reviewed workflow. Do not replace a shared
`libllama.so`, shared symlink or Ling runtime in place.

This package has no activate/start/stop/configuration commands. The selected
historical vocabulary evidence executed no inference and did not constitute
Friday product acceptance. Model loading, endpoint/protocol behavior and
performance qualification remain separate deployment checks. The observed core
hash `f973c8e1…` identifies the historical build, not a promise of identical
binary bytes across compilers, build paths or ARM CPU targets. Upstream licenses
continue to apply to source, tokenizer, model and runtime prerequisites.
