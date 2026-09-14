# Monik cumulative audit 4: custom service checks and evidence-based discovery

Date: 2026-09-14. Reviewed repository: `alinescafs3mp-afk/monik_v2`.
Exact baseline commit: `4f85de31f837dbdfeb77e9f562726de5f00b9d28`.
Exact baseline tree: `fdec03382ddab5f3ddcfe8984e9dc57a1d2788d7`.

**PRE-RELEASE SOURCE CORRECTIONS. Not a production deployment, completed native release, or claim that every defect has been eliminated.** The owner says the baseline was deployed; this audit did not access that deployment. The connector confirmed the baseline and its successful CI, then the exact archived source was rebuilt locally. The new source has not been pushed to main in this pass. No write permission error or blocked write is claimed: no source write was submitted.

This package contains one full source snapshot and one patch FROM the baseline above. That baseline already incorporates the earlier audit2/audit3 lineage. Do not apply those earlier patches again. Historical reports retained in `docs/` describe their original revisions. The latest status and evidence ledger are authoritative for this source.

## 1. Owner-visible delivery

A real check editor lives in Machine -> Services and is reachable from a service's Configure link. Save and Trial use one shared definition builder and strict server/worker validation. It supports URL, path/query, local numeric dial address, HTTP Host, TLS SNI, GET/HEAD/OPTIONS and consented POST, bounded public headers and body, secret references, per-check interval/timeout, expected status, text, typed JSON comparison, latency and explicit health vocabulary. It is not a cURL parser, shell command or raw protocol executor.

Trial is sent through the existing durable control channel and executed on the agent. It is asynchronous relative to heartbeat/control. The editor displays operation progress and sanitized result evidence, never substitutes server acceptance for a healthy service. An offline target keeps waiting even when the fleet operation says attention_required. Rejection/expiry/unknown results are not labelled a successful trial. Pending trial receipts survive restart as interrupted/unknown; they are not automatically repeated.

The draft survives telemetry refresh and switching between the machine's tabs. Route changes and browser unload warn about unsaved changes. Older configuration revisions produce a visible conflict. Editing a paused/ignored check preserves those lifecycle flags; a separately requested one-shot trial does not change the saved pause. History view is read-only for check actions. Credentials are not put into browser storage.

Existing compact machine rows, overview pins, sidebar collapse, readable chart ticks, 1/2/3/6/12/24-hour ranges, fixed history, bounded export and incident search are retained. This pass does not rewrite the dashboard framework.

## 2. Fixed defects and material design corrections

| ID | Finding / evidence | Delivered correction | Scope limit |
|---|---|---|---|
| A4-01 | Protocol/editor/runtime accepted only GET/HEAD and fixed scheduling; adding a text box alone would not execute the intended request | Versioned request fields, one builder for trial/save, strict end-to-end validation and capability gate | HTTP(S) only; no arbitrary TCP payload, gRPC, WebSocket dialogue or shell |
| A4-02 | Old agents can silently ignore additive JSON fields | `http_custom_v1` advertised by new workers, checked by server and UI before custom save/trial | Downgrade to an old worker after enabling custom configuration is not supported without restoring a compatible profile |
| A4-03 | Periodic checks were tied to the five-second collection loop; slow probes could serialize work | Per-check due times, oldest-eligible scheduling, max 16 periodic executions, no overlapping same check ID | Not an exact real-time scheduler; missed/late-run counters and large-fleet load acceptance still needed |
| A4-04 | A longer user interval could be misclassified as stale after the old fixed 15 seconds | Observations carry interval evidence; current service freshness and evaluator gap use three actual check intervals | Host contact freshness remains its separate five-second reporting contract |
| A4-05 | Long one-shot checks could block the agent control path | Durable accepted receipt before a bounded async trial; max two trials; explicit interrupted result after worker restart | Cancellation cannot recall already sent HTTP; POST may have side effects despite consent |
| A4-06 | Naive root-only probing gave little useful health advice and wildcard dial expansion mixed interfaces | Family-correct loopback normalization, bounded local candidate identification, preserved process hints, explicit unresolved coverage | Does not enumerate all proxy vhosts, container namespaces or native application protocols |
| A4-07 | Any successful common route can be a SPA/catch-all or liveness endpoint masking readiness failure | Typed body evidence, negative control, adverse-result priority, explicit review when readiness/auth/error is ambiguous | Heuristic evidence, not a proof of all dependencies or zero side effects |
| A4-08 | Updating request drafts could drop pause/ignore or drift from what Trial sent | Shared builder preserves lifecycle, identity and all request parameters; trial does not save; CAS on save | One primary check per service remains the current model |
| A4-09 | Corrupt key file could be replaced by a new random key | Invalid existing key fails without overwrite; exclusive creation; missing master key with stored ciphertext refuses startup | Recover original key from protected backup; this does not implement complete backup/restore |
| A4-10 | Malformed nonce and password-hash costs could panic | Validate encrypted record shape and bounded canonical Argon2 parameters before crypto/allocation | Not a claim of complete cryptographic/key-custody certification |
| A4-11 | Baseline race suite reproduced an exited candidate being called activated | Candidate completion and liveness update ordered under mutex; probation checks the actual child and exit notification; local request size bounded | Not independent supervisor self-update recovery; native boot/power-loss still untested |
| A4-12 | URL parser errors may contain original private query text | Redacted observation URL and generic parser failure; no raw transport URL errors returned as feedback | Public templates remain readable configuration; secret detection is heuristic, not arbitrary-content DLP |
| A4-13 | WAL NORMAL did not match the intended power-loss durability of committed control/data | FULL configured for SQLite connections and initial schema | Storage latency/device semantics must be measured; not a substitute for power-cut testing or backup |
| A4-14 | Discovery recommendations were transient rather than inspectable evidence | Additive discovery tables, bounded suggestion validation, persisted provenance/time and unresolved inventory in details | No full historical discovery version engine or complete gap-duration accounting |
| A4-15 | A configured change could leave a recent previous-rule result looking like confirmed new health | Service summary exposes pending desired/applied mismatch instead of treating the old result as current proof | Full historical rule-version evaluation is still incomplete |

Three crypto regressions were executed against the old implementation and failed before their fixes. The unmodified baseline race run also failed `TestReviewBrokenCandidateRestoresPreviousWorker`: a child that exited immediately was reported activated. The corrected test passed 30 repeated runs, and the final full race suite passed. A new advisory fixture initially omitted its JSON content type; that fixture was corrected to model its intended successful JSON endpoint, without weakening the adverse-readiness assertions.

## 3. Custom request contract

Logical schema remains version 3 with additive `omitempty` fields, so zero-valued additions do not change legacy config hashes. Custom definitions explicitly use `request_version: 1`. `http_custom_v1` is required for custom fields, non-default schedule or OPTIONS/POST. The worker still validates the complete definition before dialing.

| Field / constraint | Implementation |
|---|---|
| Methods | GET, HEAD, OPTIONS; POST requires `allow_post=true` and v1; no other methods |
| Schedule | 5..3600 seconds, multiples of 5; timeout 1..30 seconds and strictly shorter than interval |
| Body | Public text/JSON/form template or whole-body secret, mutually exclusive, <=16 KiB, POST only |
| Headers | Up to 16 public fields, <=8 KiB combined, <=2 KiB per public value; one secret header; case-insensitive duplicate and framing/header injection checks |
| Credentials | No URL userinfo; sensitive query/header/JSON/form keys rejected from public templates; header/body secret references authorized to the selected agent/check |
| HTTP result | Status, safe status text, duration, content type, bounded health vocabulary and failure layer; no raw response body |
| Expectations | Required explicit status for application checks; optional substring, dot path or JSON Pointer, typed string/bool/number/null/exists, explicit positive health and latency; all configured requirements must hold |
| JSON | Strict document EOF for configured JSON predicate, bounded pointer/values/numeric exponents, no expression evaluator |
| Transport | Numeric local IP:port at actual dial, Host/SNI separately; no environment-proxy surprise or redirect following; no automatic insecure TLS |
| Budgets | 64 KiB response body; 32 KiB response headers; total request timeout includes the bounded response exchange |
| Trial | Same validated request, durable job, no definition saved; paused/ignored schedule unchanged, but explicit diagnostic may execute once |

HEAD cannot satisfy body expectations. It may fall back to bounded GET on 405/501 for identification/baseline cases within the original deadline; feedback reports the actual method. Existing explicit insecure-TLS definitions remain visibly insecure, not newly enabled by the advisor. Per-target private CA/mTLS configuration is not added in this pass.

The UI's JSON-RPC preset is an illustrative request, not a universal JSON-RPC health method. It deliberately does not grant POST consent and defaults its interval to at least 30 seconds. Configure the method/schema from the actual application's documentation. The request builder does not execute commands, inference jobs, remediation or discovery-time POSTs.

## 4. Discovery: evidence, not green-at-any-price

First enumerate observable local TCP listeners. Wildcard IPv4 maps to 127.0.0.1; IPv6 to ::1. Preserve PID/process metadata when available. Known non-HTTP process hints (for example sshd, postgres, redis-server, mysqld) are retained as unresolved native-protocol candidates, not hit repeatedly as HTTP.

The advisor tries only a finite set of local unauthenticated GET routes: root where meaningful, `/readyz`, `/ready`, `/healthz`, `/health`, `/actuator/health`, `/livez`, `/-/ready`, `/-/healthy`, `/api/health`. There is no crawl, code execution, credential guessing, subnet scan or inference call. An auth wall at root or unverified TLS stops health advice. A root that cannot be reached is not fixed by trying different request bodies.

Recognizable JSON/text health produces a candidate with the expected future condition, not a snapshot labelled permanently healthy. Negative readiness/health is prioritized. An empty or unauthenticated/error readiness candidate blocks automatic preference for a healthy liveness alternative. HTML and empty generic 200 do not become proven application health. An otherwise plausible candidate must pass a missing-route negative control (404/410) before automatic eligibility. An all-paths-OK service, timeout or inconclusive control requires review.

Automatic configuration is allowed only for a newly discovered service with NO existing primary check, new-agent capability, explicit health predicate and sufficient evidence. Once any primary check exists, discovery does not rewrite its URL, status expectations, credentials, schedule or pause. Existing services show suggestions for preview -> trial -> explicit save.

Discovery is bounded: at most 128 candidates per pass, 8 identification workers, 25-second pass context, roughly 6 seconds per endpoint for advice and 650ms per advice request, max four retained suggestions. Advice is cached for ten minutes with a bounded cache; explicit rediscovery clears that cache. HTTP identification has its own short bounds. Budget truncation and unresolved candidates are shown rather than implying complete coverage. These are work limits, not a measured fleet freshness guarantee.

## 5. Review coverage and remaining release gaps

Source review covered protocol/config validation, storage ingestion/query boundaries, HTTP checks/trials, discovery, worker scheduling/control, secrets/auth, update probation, existing rebind/update paths, Vue draft/operation feedback, tests and packaging. Focused code corrections are listed above. Existing enrollment/spool/idempotency/history regressions run in the full suite. This is not a guarantee of exhaustive vulnerability discovery.

Still incomplete: independently recoverable service-host replacement; immutable release publication and cohort rollout orchestration; native Windows/Linux privilege/boot/recovery evidence; full protected controller backup/restore and stale-job reconciliation; long-term aggregate retention and measured capacity; custom rule/maintenance evaluator wiring; meaningful retry/resume operations; service-level multiple checks/vantage history. Existing fail-closed guards for rule.save, maintenance.set, operation.retry_selected, update.resume and supervisor self-update are retained.

Private check secrets remain memory-cached after fetch; protected offline restart persistence and robust invalidation are not newly completed. Modern check profiles require the new worker. The server-first capability rollout and downgrade boundaries in the handoff are mandatory. A same-authority URL rebind does not move SQLite or make two writers safe.

## 6. Executed validation

Go 1.27.0; Node 22.16.0; locked dependencies from the repository's toolchain snapshot; no dependency version changes. `go mod verify` passed. Baseline source tree matched the GitHub tree.

| Test | Actual outcome |
|---|---|
| Final `go test -race -count=1 -json ./...` | PASS: 144 top-level tests, 202 test/subtest pass events, 18 tested packages; zero failures |
| `go vet ./...` | PASS |
| Node frontend suite | PASS: 38 tests |
| TypeScript `tsc --noEmit` | PASS; not a separate vue-tsc/SFC typing claim |
| Vue/Vite production build | PASS |
| Linux amd64 | Server, worker, service-host and release tool built |
| Windows amd64 | All four cross-built, not natively executed |
| Candidate rollback regression | PASS: 30 repeated executions |
| Python browser script syntax | PASS |
| Actual browser walkthrough | BLOCKED before login: `ERR_BLOCKED_BY_ADMINISTRATOR` on isolated HTTPS loopback fixture |
| Expanded browser scenario | Present and syntax-valid; visual/interaction acceptance NOT RUN here |
| Native boot, power cut, full-fleet load, 24h soak | NOT RUN |
| Live owner installation | NOT ACCESSED OR MODIFIED |

The fixture uses real Go API/storage and compiled Vue with synthetic telemetry. It is not a substitute for a real agent/OS fleet. The baseline's successful remote browser CI does not validate this source's new editor. Full validation logs and packaging reproduction checks are in the delivery archive. Audit binaries were locally compiled but are not distributed as signed production releases.

## 7. Primary references

References checked on 2026-09-14. They support protocol constraints, not a certification of Monik. Product budgets and implementation facts are established by this source/tests.

- RFC 9110, HTTP semantics, methods, authority, response codes: `https://www.rfc-editor.org/rfc/rfc9110.html`
- Go HTTP client deadlines and transport behavior: `https://pkg.go.dev/net/http`
- Spring Boot Actuator health endpoint and status mappings: `https://docs.spring.io/spring-boot/reference/actuator/endpoints.html`
- Prometheus health versus readiness endpoints: `https://prometheus.io/docs/prometheus/latest/management_api/`
- SQLite WAL and synchronous durability: `https://sqlite.org/wal.html`
- TUF specification, retained release requirement: `https://theupdateframework.github.io/specification/latest/`
