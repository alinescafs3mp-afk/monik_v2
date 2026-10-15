# Mistral native compiler cache

Only the fixed `mistral-small-4-119b-heretic` profile receives an additional
private tmpfs from the generic supervisor:

```text
/tmp/spark-compiled-cache:rw,exec,nosuid,nodev,size=2147483648,uid=1000,gid=1000
```

Native TorchInductor/Triton compilation loads generated shared objects. The
generic Docker `/tmp` tmpfs retains its existing noexec default; it cannot load
those objects. This 2 GiB exception permits executable mappings only in the
private compiler-cache mount. It is container-local and disappears with the
container; it does not persist compilation between runs. Its maximum allocation
fits within the existing Mistral runtime-workspace budget; the declared memory
budget and preflight checks remain unchanged.

The Mistral manifest row must separately receive exactly these existing allowed
environment values through the normal policy approval and preparation workflow:

```json
{
  "XDG_CACHE_HOME": "/tmp/spark-compiled-cache/cache",
  "TORCHINDUCTOR_CACHE_DIR": "/tmp/spark-compiled-cache/torchinductor",
  "TRITON_CACHE_DIR": "/tmp/spark-compiled-cache/triton",
  "FLASHINFER_WORKSPACE_BASE": "/tmp/spark-compiled-cache/flashinfer"
}
```

`HF_HOME=/tmp/hf` and the engine command remain unchanged. The supervisor does
not rewrite or silently supply these manifest values. Installing the source
alone therefore does not complete the correction. The exception cannot be
enabled for another model or resized through manifest runtime fields. All
existing model mounts remain read-only, with the existing user, capabilities,
root filesystem, resource limits, collection lock and stop logic.

Run `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -v test_mistral_compiler_cache`
from `python/`. These checks cover exact command construction, four unchanged
generic profiles, direct legacy delegation, adverse configuration inputs and
deployment/source hash consistency. They do not prove native shared-object
mapping or model startup. Actual-image CPU mapping controls, reapproval of the
complete policy, all seven profile preparations and a fresh native Mistral start
remain required before claiming runtime acceptance.
