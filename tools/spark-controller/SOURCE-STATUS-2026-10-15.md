# Source checkpoint: 2026-10-15

This checkpoint publishes reproducible source and bounded observations. It does
not install a runtime or declare a model accepted for Friday. Raw requests,
private evaluation corpora, credentials and runtime instance identifiers are
excluded.

| Component | Included source and observed boundary | Still separate |
| --- | --- | --- |
| Nemo metadata and Unicode | [Pinned repair, complete patch and ARM64 build recipe](nemotron-tokenizer-fix/README.md). The installed correction preserved the tensor payload. Historical vocabulary-only qualification covered 145,820 rows with zero candidate failures; both old-core and old-metadata negative controls are retained. | The private full corpus is not redistributed. Rebuilding with another toolchain needs new native qualification; a vocabulary pass does not establish inference quality. |
| LAN model contract | [Final Go source and controls](lan-api/README.md). Ling and Nemo chat routes require exactly one correctly typed, exact model name. Complete invalid Nemo model bodies receive a fixed JSON error; incomplete uploads retain the generic error. Accepted bodies and SSE remain unchanged. Native Nemo identity, authentication and wrong-model JSON controls were observed; duplicate/type/escape and forwarding controls are offline tests. | Model validation is limited to the documented routes. It does not establish cancellation, model capacity or product acceptance. |
| GPT fixed declarations | [Seven exact precision/FP8-scale/cache/stream-helper rows](GPT-APPROVED-DECLARATIONS.md) and their real manifest/container/seal tests. Source approval accepts only complete fixed rows. | Each selected row needs its own ordinary policy approval, integrity preparation and actual runtime/quality qualification. A declared cache directory is not measured cache reuse. |
| Mistral compiler cache | [Fixed private executable tmpfs and four explicit cache variables](MISTRAL-COMPILER-CACHE.md). Historical CPU-only checks in the pinned image reproduced the old mapping failure and loaded a generated shared object from the proposed mount. | This is an ephemeral 2 GiB cache. Source/CPU mapping checks do not prove a healthy native model start, speed or quality; complete deployment approval and native qualification remain separate. |

The [Nemo launch example](NEMOTRON-PROVISIONAL-EXAMPLE.json) records the tested
single-slot selection: 262,144 tokens, batch 2,048, microbatch 1,024 and
`--cache-ram 16384`. It is a documentation example, not a controller manifest row
or automatic deployment. The GGUF remains MXFP4_MOE; metadata repair does not
convert its tensor precision to BF16. The model/core/image pins and high request
sampler are explicit in the example.
The 8,192 output and 2,048 history reserves belong to the isolated client
qualification; they add no global server output cap or client budget override.

A sequential cache0/cache16 comparison observed twelve completed three-turn
context tasks, 36 HTTP requests in total, with native full-body counts matching
usage and the synthetic tool/correction/history oracles passing. First-request
bodies matched between arms; generated follow-up bodies and output work differed.
Returning histories showed cached-token reuse, while first visits did not show
that benefit. This supports the provisional selection for that workload. It does
not establish a strictly cold randomized comparison, global optimum, allocated
cache bytes, sustained-load stability, two-slot admission or cancellation
isolation. Broader Hermes/Friday acceptance is unclaimed.

Reproduce the source checks with the focused commands in the linked documents.
Python controller tests inject host observations; Go tests use local test
transports; metadata guards use tiny synthetic GGUF files. A source-only test
pass must stay distinct from installation, native execution and acceptance.

The subsequent [16 October native correction checkpoint](NATIVE-CORRECTIONS-2026-10-16.md)
adds the exact Harmony stream-prefix correction, GPT stop-release observation
handling, fixed midrange catalog recognition, and a blocked Mistral correctness
example. Read its explicit installation/runtime distinction before using it.
