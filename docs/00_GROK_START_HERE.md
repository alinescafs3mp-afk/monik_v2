# Grok: apply cumulative Audit 10

Repository: `alinescafs3mp-afk/monik_v2`. Base: `ce602d01b3bc335a94b194bf1c3c04e7e46a25ad`. Base tree: `9a5c10dc7e9640ae3d442a65f444d3fb4b917b1e`.

This package contains complete source plus ONE patch against the already integrated Audit 9 baseline. Do not apply Audit 1-9 patches again. Read `AUDIT_REVIEW_10_2026-09-14.md` and `V10_RELEASES_RU.md`. This source snapshot is not a signed production release and does not prove native upgrade recovery.

1. Verify `SHA256SUMS` with `python3 verify_package.py`. Run `python3 preflight.py /path/to/repository`; it is read-only. Preserve all concurrent user changes. If main is not the exact baseline tree, integrate deliberately and retest; never force-reset or extract a ZIP over a dirty repository.
2. In a clean matching worktree run `git apply --check /absolute/path/monik-audit10-fixes.patch`, then apply it and review the complete diff. No partial tree should be deployed.
3. Build/test matching frontend first: `cd web && npm ci && npm test && npx --no-install tsc --noEmit && npm run build`. From root run `go test -race -count=1 ./...`, `go vet ./...`, `go mod verify`, `make dist`. Use the Go version in go.mod. `make dist -o ui` was used by the auditor only AFTER the matching UI was separately built.
4. Run existing compiled-process fixtures with MONIK_NATIVE_AGENT_BIN and MONIK_NATIVE_SUPERVISOR_BIN pointing to the actual newly built Linux programs. Run browser CI and the expanded Updates assertions; current auditor browser attempt was blocked before login, not passed. Inspect screenshots, overflow/focus and console errors without weakening tests.
5. Validate actual native update on a disposable system first. New metadata paths require immutable_release_v1. Deploy server/UI, upgrade existing agents locally once without reenrollment, confirm capabilities, then re-import signed bundles to immutable catalogue rows and test a single chosen managed target. Do not remove the older-agent gate or quietly use mutable fallback.
6. Publish a coherent reviewed commit directly to main as authorized. Preserve existing registrations, protected state, trusted roots, selected URLs and legacy published files. A source commit is not permission to restart production unexpectedly.

## Compatibility and rollback

Legacy release rows remain visible but cannot start new updates. Legacy directories are frozen; new publication uses content-addressed paths plus SQLite release_publications/release_trust. Once a new publication commits, DB trust is authoritative; legacy files are not advanced. Preserve BOTH DB and complete release/trust directories. A temporary older controller may continue some monitoring but does not understand the new catalogue. Do not use an older controller to publish updates or reset trust; fence writers and follow a deliberate coordinated recovery plan. The full automatic restore remains unfinished.

Internal preparing jobs are never delivered until all immutable parameters are committed. Interrupted preparation expires under the original deadline; this is not a batch-resume implementation. Keep explicit unavailability for retry_selected, update.resume and independent service-host self-update.

## Evidence

Current evidence is in evidence/ and docs/audit/validation-review10-2026-09-14.json. 254 top-level Go tests / 322 test-subtest pass events / 19 tested packages; 96 Node tests; two separate UID65534 native-process tests. Native OS boot, Windows SCM, power loss, actual TV/browser and full fleet/soak remain unproven. The four new before-fix reproductions and the corrected intermediate conflict regression are retained as evidence, not hidden.
