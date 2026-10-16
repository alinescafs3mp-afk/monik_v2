# Fixed Spark V4 LAN ingress

This is the existing authenticated Go proxy with eight fixed ports, with an exact model
contract on **POST `/v1/chat/completions` at ports 31002, 31004 and 31006–31008**. The
request requires exactly one top-level string `model` equal to
`ling-3-flash-124b-heretic-text` on 31002 or
`nemotron-3-super-120b-heretic-v2` on 31004; ports31006–31008 use
`glm45-air-106b-derestricted`, `gemma4-31b-deckard-heretic`, and
`nemotron-super-49b-v15-abliterated` respectively. Missing, wrong, nonstring or duplicate names,
invalid JSON and trailing JSON/text return a fixed HTTP 400 before contacting the
engine. Escaped top-level keys are checked after JSON decoding, so spelling a
second key as `mo\u0064el` does not bypass duplicate rejection. Nested `model`
members do not count as the requested endpoint.

On ports31004 and31006–31008, a completely read body that fails this model contract receives
an OpenAI JSON error envelope (`invalid_request_error`, `model_not_found`,
`param: model`). Incomplete uploads retain the generic HTTP 400 response.
Port 31002 retains its existing fixed-text refusal format.

The accepted upload is forwarded byte for byte. There is no model rewrite,
fallback, request retry or endpoint selection. The original headers, credential
stripping, eight fixed targets, SSE streaming, response statuses and cancellation
behavior remain. Other ports and routes retain their original transparent
behavior, including other routes on ports 31002 and 31004. This is intentionally an exact
chat-route contract rather than a claim of model-name validation on every API.

The existing 64 MiB individual request limit and 120-second upload deadline are
preserved. Complete validation precedes forwarding for these fixed chat routes.
All validated chat/count routes share one **64 MiB aggregate admission budget**,
held until forwarding completes; saturation returns HTTP 503 without contacting
the engine. Consequently, two concurrent 40 MiB Ling/Nemo chat bodies are not both
admitted. Other routes remain streamed and do not consume this new byte budget.
The original limits of 16 active requests, 64 connections and 32 KiB headers are
unchanged. No temporary spooling, added service, filesystem permission or larger
systemd memory limit is required.

`encoding/json.Valid` validates the full document. A small scan of the already
valid JSON locates top-level members without allocating messages or large
unknown strings. Only short candidate `model` keys/values are decoded. The
production path does not unmarshal the complete request into maps or copy large
field values. The scanner is checked against an independent standard-library
decoder oracle by the included fuzz test.

Run tests and produce the static ARM64 binary with Go 1.27.2:

```sh
go test -race -count=1 lan_proxy.go lan_proxy_test.go ling_model_test.go nemo_model_test.go midrange_test.go gemma_count_test.go
GOMAXPROCS=2 go test -fuzz=FuzzExactLingModel -fuzztime=20s -run='^$' \
  -parallel=2 lan_proxy.go lan_proxy_test.go ling_model_test.go nemo_model_test.go midrange_test.go gemma_count_test.go
GO111MODULE=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags='-s -w -buildid=' -o spark-v4-lan-api lan_proxy.go
```

Installation is a separate root-owned operation. Preserve the existing binary,
unit mode/owner, private environment file and controller locks. This proxy has
its own `spark-v4-lan-api.service`; the controller's built-in revision list does
not include its ELF. Inspect the current external policy and integrity references
before replacement, then update any actual executable pins and dependent native
metadata through the approved preparation procedure. Source inspection does not
establish which pins exist on the host. Restart only `spark-v4-lan-api.service` after
the previous invocation and its requests are accounted for. Do not rerun the
historical hardcoded first-install script. Validate the installed executable
hash and all fixed listeners, then run authenticated positive/negative model,
stream and cancellation checks. Offline tests do not establish live acceptance.
This work does not change any model launch configuration or the separate
RTX5090 endpoint.

The separate Gemma-only count alias accepts authenticated POST `/v1/tokenize` on
31007 and forwards to native `/tokenize`, preserving the complete body and query.
It requires exactly one correct model, rejects escaped path aliases and GET, and
shares the existing byte/request/upload bounds. Other ports keep `/v1/tokenize`
transparent; bare `/tokenize` stays outside the LAN surface. This source change
is not installed merely by staging the earlier ten-profile package.
