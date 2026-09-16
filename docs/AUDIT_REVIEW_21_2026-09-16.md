# Monik V21 audit: clean TV presentation and access correctness

## Provenance

Reviewed commit `3303e991a2624c945662355e22fa7cfb8ef06a6a`, tree `f319e9b12f6e4091a95dea162046f1e509fe2078`. The local checkout was reconstructed from the V20 source snapshot plus the three GitHub-verified host integration files. Its complete root Git tree equals the requested commit's tree. The local reconstructed commit is not represented as an upstream commit.

V20's GitHub Verify Monik run was successful; its host ledger reports 45 browser cases. Both are baseline evidence, not acceptance of this patch. Original host ledger/status are preserved under docs/history/*_THROUGH_V20.md.

## Requested UI changes

A local clean-TV choice removes the lower profile, width controls and pager. It is deliberately independent of User-Agent and the controller-wide TV profile. Width/density/autoplay synchronization is still active. The top toggle reverses it, diagnostics remain at the top, and multi-page arrows move above the rows. The local setting persists, does not publish layout, and an explicit URL flag cannot silently undo later user choice on reload.

The existing TV console and detail routes use two 30px square native links with inline SVG, text alternatives and visible focus. The measured grid reserves 30px instead of 7rem. No native terminal or network authority changed. In clean mode a parked pointer does not stop paging; keyboard focus still does. Published layout changes still apply while local editing is disabled.

## Reproduced defects

### A. Viewer could not sign out

The mutation guard rejected every viewer POST, including the user's own logout. The new exception is exact method/path only and still passes cookie, CSRF and global origin policy. Router tests verify own-session deletion, retained other-session access, forbidden unrelated mutations and rejected bad-CSRF/cross-origin requests. A storage failure remains an error and does not pretend server-side logout succeeded.

### B. Delayed export retained old authorization

A request admitted by middleware could wait, lose its session/role, then receive the finished export. Three baseline cases reproduce revoke, expiry and demotion. The handler revalidates before file access and before releasing bytes, and rechecks artifact expiry. The existing positive export test now uses a real stored session rather than a fabricated struct. Existing owner/requester restrictions and integrity checks remain.

### C. Export storage failure appeared as missing artifact

A failed operation lookup was conflated with absent export. It now yields a service error, not a misleading missing-file result. No error path serves unverified bytes.

### D. Delayed credential registration could stage a successor using retired authority

The endpoint authenticated before reading its body and used an unconditional overlap setter afterwards. Delaying the body until after token rotation or revocation produced HTTP 200 and a new pending record on the baseline. Registration now repeats authentication and uses a serialized transaction to compare the authenticator and revoke state at the write boundary. A different live candidate cannot be overwritten, and the same verifier/job retry returns the existing expiration instead of extending the overlap.

This is distinct from V20's pending-key promotion fix: V21 fixes creation of the pending record. Existing positive registration/promotion is tested. No agent binary or schema update is required.

### E. Malformed credential candidate accepted

The endpoint checked string length but not SHA256 hex or the nonempty job reference. Canonical lowercase hex and bounded nonempty job references are now required; the existing agent already emits them. No verifier is returned in a diagnostic response.

These five groups comprise nine failing baseline cases. The concurrency, normal-path and write-failure tests additionally validate the replacement behavior. They are not counted as nine anonymous exploit paths.

## Review and test scope

Focused manual review: display state, common TV profile interaction, measured columns/actions/paging, browser roles/logout, bounded history export and pending-credential lifecycle. The full available automated suite covers controller/storage, ingestion, discovery, configured checks, histories, operations, TUF/rollout, local runtime, IPC, terminals and frontend. This is not a claim of a new line-by-line examination of every historical subsystem or a live external penetration test.

New UI tests execute the production useDisplay module with isolated stores and compile/render real Vue templates. They prove state/markup contracts, not real pixel geometry. New Playwright scenarios cover two independent sessions, passive sync, no layout writes, errors, top-only paging and 30px buttons at all TV densities. Auditor Chromium was blocked before login, so zero new browser cases are credited.

## Final results

See evidence/validation-summary.json for final-source exits/counts. Preliminary logs are retained separately and must not be substituted for the final run after the credential fix. Full Go race, frontend, static checks, matching cross-builds and native process tests are recorded there. The supplied verification script is scripts/verify-audit21.sh --with-browser.


| Final-source check | Result |
|---|---|
| Go race | 477 top-level, 713 test/subtest/seed PASS, 27 tested packages |
| Frontend | 198 Node tests, TypeScript and Vite PASS |
| Repeats | V21 server/storage 10 shuffled race runs; 46 UI tests x10 |
| Static/integrity | go vet and go mod verify PASS |
| Build | Linux/Windows amd64 four binaries each; installer templates amd64/arm64 |
| Native Linux | 3/3, UID65534, including signed update and failed-candidate recovery |
| Console UID / systemd syntax | Both real UID cases PASS; unit syntax PASS, not activation/boot |
| Browser | BLOCKED_BEFORE_LOGIN, 0 passed, five new scenarios authored |

The first UID-fixture invocation used a wrong opt-in name and skipped. The correct `MONIK_NATIVE_TESTS=1` invocation subsequently ran both cases; both logs remain available. No skipped run is counted as proof.

## Deployment and remaining gates

Deploy matching controller + embedded UI and rebuild matching installer templates for future downloads. Existing agent identities, current credentials, state directories, URLs, monitoring choices and console opt-in remain untouched. There is no automatic key rotation or enrollment in this patch.

Physical TV/Yandex rendering, systemd/Windows service boot, external ingress and firewall topology, full protected backup/restore, independent service-host update recovery, fleet load and 24h soak remain open. Hiding controls is not a wallboard credential. Maintain the authoritative RELEASE_COMPLETION_PLAN and do not mark these older gates complete because a UI patch passed unit tests.
