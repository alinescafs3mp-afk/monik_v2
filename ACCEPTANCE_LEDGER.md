# Acceptance ledger: Audit5

Exact baseline `37dc23405606b24a82a629533311c19dae577a2a` / tree `a3e00db6ddefa2fb2c2751e022d6a1cd1b2be7a9`. This table describes the accompanying code, not historical integration claims.

| Check | Result |
|---|---|
| Before-fix regression reproduction | 2 targeted failures on baseline, both fixed |
| Go race | PASS: 158 top-level tests, 216 test/subtest events, 18 packages |
| Go vet | PASS |
| Node UI tests | PASS: 47; includes behavior tests and explicitly identified source contracts |
| TypeScript / Vue build | PASS / PASS; no vue-tsc claim |
| Linux amd64 commands | All four built |
| Windows amd64 commands | All four cross-built, native execution NOT RUN |
| Native agent arrival HTTP test | Actual isolated TLS + SQLite + setup flow + owner operation + first report; PASS |
| Browser syntax | PASS |
| Current browser execution | PASS: 14/14 Playwright scenarios on this host against the synthetic loopback fixture (grouping, repeated configure, rename through telemetry, history-failure isolation, header layout, custom POST trial pending). Auditor environment was blocked before login. |
| systemd/SCM native install/boot/recovery | NOT RUN; existing provisioning gaps recorded |
| Independent supervisor recovery/full restore/long aggregates | NOT COMPLETE |
| Full v3 battery/50-agent load/24h soak | NOT RUN |
| Owner production | Not accessed by the audit package. Integration deploys the matching commit onto the existing LAN controller/worker without re-enrollment. |

Go 1.27.1, Node 22.16.0 with existing locked dependency snapshot. The auditor tree is `5aa042f64ec638e4effb07093fa9c7ce39e9ffe9`; this host additionally waits for history-backed charts and asserts the compact header. Git patch/tree reproduction is checked separately in that archive's `PACKAGE_MANIFEST.json`.
