# Isolated ARM64 core build

This is a documented adaptation of the successful build attempt, not an installer
or an automatically executed script. All writes below target one new job
directory. Supply the **complete clean** upstream checkout at revision
`df18bee11e96557b07aa7b12db0976f3c7ac9165` and the exact existing ARM64 runtime.
Do not use an audit's partial source copy or an existing CMake cache.

Use the already available ARM64 environment
`lmsysorg/sglang@sha256:9d2a843c706c74bc259c0d9abf360551eb2734e1e7d255ab012a6965f10480b6`:
Ubuntu 24.04, g++ 13.3.0, CMake 3.31.1, Unix Makefiles, Release. The observed
build used no network, no GPU/device mounts, non-root UID 1000, 4 allowed CPUs,
6 GiB RAM with no swap, pids limit 256 and two build jobs. Its existing source
and runtime mounts were read-only. Preserve a measured resource budget and
finite outer timeout; the successful compile/check slice took 113 seconds.
No CUDA or GGML kernels are rebuilt. No model files are needed for this build.

## Stage fresh output and verify inputs

Paths below are operator-supplied placeholders. `NEMO_JOB` must be a nonexistent
path outside the input source and runtime trees. Use absolute paths available
inside the chosen build environment. `mkdir` refuses an existing job path.

```bash
set -eu
NEMO_PACKAGE=/absolute/path/to/nemotron-tokenizer-fix
NEMO_INPUT_SOURCE=/absolute/path/to/clean-llama.cpp
NEMO_INPUT_BIN=/absolute/path/to/existing-runtime/bin
NEMO_JOB=/absolute/path/to/new-nemo-build-job

test "$(git --no-optional-locks -C "$NEMO_INPUT_SOURCE" rev-parse HEAD)" = \
  df18bee11e96557b07aa7b12db0976f3c7ac9165
test -z "$(git --no-optional-locks -C "$NEMO_INPUT_SOURCE" status --porcelain=v1 --untracked-files=all)"
python3 -I "$NEMO_PACKAGE/verify_inputs.py" source --root "$NEMO_INPUT_SOURCE" --state before
python3 -I "$NEMO_PACKAGE/verify_inputs.py" runtime --root "$NEMO_INPUT_BIN" --state original
mkdir -- "$NEMO_JOB"
mkdir -- "$NEMO_JOB/source" "$NEMO_JOB/bundle" "$NEMO_JOB/bundle/bin"
cp -a -- "$NEMO_INPUT_SOURCE/." "$NEMO_JOB/source/"
cp -a -- "$NEMO_INPUT_BIN/." "$NEMO_JOB/bundle/bin/"
python3 -I "$NEMO_PACKAGE/verify_inputs.py" source --root "$NEMO_JOB/source" --state before
test -z "$(find "$NEMO_JOB/source" -name CMakeCache.txt -print -quit)"
(cd "$NEMO_JOB/source" && patch --batch --fuzz=0 -p1 \
  -i "$NEMO_PACKAGE/nemotron-exact-tekken.patch")
python3 -I "$NEMO_PACKAGE/verify_inputs.py" source --root "$NEMO_JOB/source" --state after
git --no-optional-locks -C "$NEMO_JOB/source" diff --stat
git --no-optional-locks -C "$NEMO_JOB/source" status --short
```

The entire actual checkout is copied, including Git metadata; no existing index
is refreshed or rewritten during read-only checks. Exactly these source paths
may change: `src/llama-vocab.cpp`, `src/unicode.cpp`, `src/unicode-tekken.h`,
`src/unicode-tekken-data.h`. Compare status/diff with that list before building.
The manifest checks exact before/after content of all four patch paths. The
four pinned GGML 0.26.0 DSOs and every runtime symlink are checked separately.

## Configure and inspect the actual target graph

```bash
export GIT_OPTIONAL_LOCKS=0 OMP_NUM_THREADS=2 OPENBLAS_NUM_THREADS=2
export CUDA_VISIBLE_DEVICES='' NVIDIA_VISIBLE_DEVICES=void
cmake -G 'Unix Makefiles' -S "$NEMO_JOB/source" -B "$NEMO_JOB/build" \
  -DLLAMA_USE_SYSTEM_GGML=ON -DBUILD_SHARED_LIBS=ON \
  -Dggml_DIR="$NEMO_PACKAGE/import-ggml" \
  -DNEMO_PINNED_SOURCE="$NEMO_JOB/source" \
  -DNEMO_PINNED_GGML_BIN="$NEMO_JOB/bundle/bin" \
  -DLLAMA_BUILD_COMMON=OFF -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_TOOLS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=OFF -DLLAMA_BUILD_APP=OFF \
  -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF -DLLAMA_OPENSSL=OFF \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_BUILD_WITH_INSTALL_RPATH=ON \
  '-DCMAKE_INSTALL_RPATH=$ORIGIN' -DCMAKE_INSTALL_RPATH_USE_LINK_PATH=OFF \
  -DCMAKE_BUILD_RPATH_USE_ORIGIN=ON -DCMAKE_EXPORT_COMPILE_COMMANDS=ON \
  -DGGML_CCACHE=OFF
cmake --build "$NEMO_JOB/build" --target llama -- -n > "$NEMO_JOB/llama-dry-run.txt"
python3 -I - "$NEMO_JOB" <<'PY'
from pathlib import Path
import json, sys
job = Path(sys.argv[1]); source = (job / 'source').resolve()
commands = json.loads((job / 'build/compile_commands.json').read_text())
vendor = {'vendor/hash/hash.cpp', 'vendor/hash/xxhash/xxhash.c',
          'vendor/hash/sha1/sha1.c', 'vendor/hash/sha256/sha256.c'}
seen = set(); cpp = 0
for command in commands:
    file = Path(command['file']).resolve()
    relative = str(file.relative_to(source))
    if file.suffix == '.cpp' and relative.startswith('src/'):
        cpp += 1
    else:
        assert relative in vendor, ('unexpected global compile input', relative)
        seen.add(relative)
assert cpp == 43 and seen == vendor and len(commands) == 47
dry = (job / 'llama-dry-run.txt').read_text()
assert 'nvcc' not in dry and '/ggml/src/' not in dry
assert '/vendor/hash/' not in dry and 'vendor-hash.dir' not in dry
print('PASS: 43 C++ core inputs; 4 unrelated vendor entries are not built by target llama')
PY
```

Global `compile_commands.json` contains four unrelated vendor/hash entries because
upstream CMake defines that static target unconditionally. They caused the first
historical build harness to reject the graph **before compilation**. Do not
silently remove the check or build them: require their absence from the actual
`llama` dry build. Reject `.cu`, nvcc, GGML compilation or any unexpected input.

## Build and assemble the new bundle

```bash
cmake --build "$NEMO_JOB/build" --target llama --parallel 2
cp -- "$NEMO_JOB/build/bin/libllama.so.0.6.0" \
  "$NEMO_JOB/bundle/bin/libllama.so.0.6.0"
python3 -I "$NEMO_PACKAGE/verify_inputs.py" runtime \
  --root "$NEMO_JOB/bundle/bin" --state rebuilt
python3 -I "$NEMO_PACKAGE/verify_inputs.py" runtime \
  --root "$NEMO_INPUT_BIN" --state original
python3 -I "$NEMO_PACKAGE/verify_inputs.py" source \
  --root "$NEMO_INPUT_SOURCE" --state before
```

Only the core in the newly copied bundle is replaced. The 23-entry inventory must
retain all other 22 entries exactly: 8 real binaries and 14 symlinks. The original
`libllama.so → libllama.so.0 → libllama.so.0.6.0` chain is retained.
The rebuilt-state checker permits different core bytes and prints their SHA;
it does not substitute an ABI or native tokenizer acceptance check.

## Check ABI and dependencies before selection

Inspect the old and new core with `readelf -W -h -d -V` and compare their defined
exports using `nm -D --defined-only --format=posix`. These are read-only tools;
do not execute a model or server as a build check. Required acceptance:

* ELF AArch64, SONAME `libllama.so.0`; exact same defined export names (observed
  5,884; zero missing or added).
* Identical DT_NEEDED and required GLIBC/GLIBCXX/CXXABI versions. The observed
  DT_NEEDED set is `ld-linux-aarch64.so.1`, `libc.so.6`, `libgcc_s.so.1`,
  `libggml-base.so.0`, `libggml.so.0`, `libm.so.6`, `libstdc++.so.6`.
* New core RUNPATH exactly `$ORIGIN`, with no build/input runtime fallback.
  Other copied DSOs retain their historical byte-identical `$ORIGIN:` RUNPATH;
  this package does not claim to have hardened those unchanged binaries.
* Every public header is unchanged. No GGML or CUDA object was rebuilt, and all
  reused binaries/symlinks match `runtime-original.json`.
* In a prepared environment, `ldd` of the new `bundle/bin/llama-server` resolves
  dependencies from the new bundle/system environment, with no original runtime
  fallback. Missing system driver/CUDA libraries are a blocker for native load,
  not proof of an ABI regression or success. The CPU-only build environment
  lacked `libcuda.so.1`; `--version` was intentionally not executed there.

The observed new core SHA is recorded in `runtime-observed-candidate.json` and
`PINS.json`. Compiler, path and CPU differences may change it; validate and record
your new artifact instead of silently accepting the historical hash. This is
source/build-recipe reproducibility, with no compiler-independent bitwise claim.

Native vocabulary qualification and explicit Nemo-only deployment follow the
separate acceptance boundary in [README.md](README.md). Never copy this bundle
over the original runtime or retarget a shared Ling symlink.
