# Monik Audit 17: resizable overview and interface reliability

Date: 2026-09-15. Baseline: `35fccbb0265777b12c2893667ef2fea33e953928`; baseline tree: `aa0cdf94c9b07a60df715463b863cfe0721052f0`.

## Scope and authority

The owner requested draggable Overview parameter boundaries, adaptive contents, a self-review and useful improvements. Work was performed on an exact source artifact for the baseline commit, not on production. No GitHub writes, runtime API mutations, agent deployment, certificate replacement or service installation were performed. This is a targeted frontend/reliability review with a full existing backend regression run, not a claim that every execution path or remaining release gate was re-proven.

The source snapshot contains all previously integrated changes. The single patch applies to the stated baseline. Do not apply older cumulative patches again. Go sources, schema, dependencies and wire capabilities are unchanged in this audit.

## Implemented interaction

`overviewColumns.ts` defines validated numeric preferences and constrained adjacent-track geometry. `useOverviewColumns.ts` owns measured width, mode-local persistence, pointer capture and cancellation. Both normal rows and TV rows use the same components and all rows on a surface use one grid definition. Five handles separate the six semantic columns; the normal row's action area remains reserved.

Preferred widths are stored as rem values in schema version 1, separately under `monik:overview-columns:v1:auto`, `:compact` and `:tv`. Stored data is bounded and validated; arbitrary strings cannot become CSS. The last Services column takes remaining width. Shrinking the viewport fits first-five tracks toward minima without overwriting preferences; below the combined minimum the interface switches to a stacked layout. No zoom transform or disabling of user zoom was introduced. The only transform centers the small pointer handle on a grid boundary.

Dragging uses primary pointer capture, updates the two adjacent tracks and commits local preferences on release. Escape, pointer cancellation, lost capture, changed content dimensions, window blur/visibility loss and unmount cancel unfinished interaction. A global completion fallback cleans up a captured row removed remotely. Keyboard separators in the common header support arrows, Shift, Home and End and report bounds/current width. Per-row handles remain pointer-operable without adding five Tab stops per host. Storage failure produces a visible notice while preserving working in-memory layout.

Telemetry remains live. Stable row identity/order is held during a pointer gesture, and normal priority resumes afterwards. TV paging/capacity changes do not interrupt dragging; a deferred new-problem jump is performed afterwards. Geometry reads and fitting are coalesced with requestAnimationFrame. The existing service ResizeObserver rebuilds the three-row columns as available space changes.

## Verified baseline regressions

`web/test/audit17-regressions.test.mjs` can execute the baseline source using `MONIK_TEST_BASELINE`. It transpiles/exercises the exact SFC handlers with controlled HTTP promises and imports the exact pure functions; it does not implement a substitute copy of the behavior. Seven cases fail against baseline and pass against V17:

| ID | Defect | Correction |
|---|---|---|
| R17-01 | Earlier graph-point response could replace a later selected point. | Generation, selected timestamp, mode and machine must still match before publishing. |
| R17-02 | A response for a previous machine could populate the current page. | Bind point response to the captured machine identity and invalidate on navigation. |
| R17-03 | A late error from an older selection could overwrite a newer success. | Ignore stale errors as well as stale data; loading ends only for its own ticket. |
| R17-04 | Nonfinite service-column measurement produced nonfinite capacity. | Invalid width measurement uses the finite bounded default. |
| R17-05 | Reconciled/accepted but unfinished export was announced as ready. | Readiness toast requires the validated download URL; otherwise show awaiting confirmation. |
| R17-06 | Overview priority could use old reported failure while the lamp had aged to stale. | Sorting/highlighting and service ordering use the same server-anchored current state. |
| R17-07 | TV capacity assumed a fixed footer height even when controls wrapped. | Pass measured footer/notice occupancy into the capacity calculation. |

Related corrections invalidate pending point reads on explicit date/range changes, avoid a slower routine history load overwriting a newer selected point, and clear unsaved secret input when moving between machine identities. These are code-reviewed additions, not seven additional independent reproductions.

## Self-review of the new code

The first implementation inherited two conflicting CSS rules: a more specific compact-row padding declaration and a legacy TV-header grid. Explicit scoped priority now ensures the header and rows use the same measured tracks and insets. The absolute handle overlay also must use the full row padding edge, not an explicitly placed content-only grid area; otherwise padding would be added twice. Source-structure guards and numerical checks were added, along with an actual browser coordinate assertion. The source guards are not presented as visual proof.

The implementation was additionally tested with fractional font sizes, extreme deltas, viewport shrink/restore, invalid persisted preferences, blocked storage, unrelated pointer IDs, cancellation and remote removal of a captured row. Wide TV preferences are allowed within a finite bound and safely fitted when returning to a smaller viewport.

An old test that asserted the exact spelling of `overviewPriority(c,fresh(c))` was updated to assert the new current-state callback and continued problem-class binding. A behavioral baseline regression separately verifies the new semantics. Existing browser expectations were not removed or made optional to manufacture a green run.

## Validation and evidence

See `evidence/validation-summary.json` for actual final counts, exit codes and limitations. The final suite includes the full Go race tests, go vet, module verification, full Node tests, TypeScript, Vite, Linux/Windows builds and both Linux installer-template architectures. Three opt-in compiled Linux worker/supervisor scenarios are run separately, including genuine local signed replacement and failed-candidate recovery under uid 65534. Go and installer production code were not changed; these runs are regression evidence, not new native-service acceptance.

Fourteen column tests include pure geometry, real Vue lifecycle/composable execution with mocked browser layout APIs, and narrowly identified source assertions. The seven before/after tests use deferred promises and real source handlers/functions. Ten repeated frontend interaction runs check independence and cleanup. No new mutation-fuzz run is claimed in this audit.

The browser attempt stopped at initial loopback login with `ERR_BLOCKED_BY_ADMINISTRATOR`. It did not execute any authenticated V17 scenario. `tests/browser/column_scenarios.py` is integrated into the existing fixture battery and checks pointer dragging, keyboard changes, Escape, reload persistence, cross-row/header/handle geometry, service columns of three, TV footer fit, isolated preferences and mobile reflow. It must pass on the host before visual acceptance. Physical Yandex/TV, Firefox/Safari, 200% user zoom and actual native boot remain independent checks.

The actual Go baseline pass count differs by one from the final run because the source artifact initially has no built UI: its missing-asset test skips before Vite and runs afterwards. Opt-in native/ICMP skips are enumerated, not counted as successes. `tsc --noEmit` is not `vue-tsc`; Vite compiles the Vue SFCs but is not full SFC type-checking.

## Deployment and rollback boundaries

Deploy matching server and embedded UI. No deployed worker replacement is required for V17's UI changes; rebuild matching credential-free installer templates when publishing a new server build. Preserve all runtime state, real endpoints and trusted keys. Preferences can be reset independently of server data. No DB migration is introduced here; that does not relax existing restrictions on controller downgrades during active V11+ rollout plans.

The complete protected restore, independent service-host recovery, native systemd/SCM/boot, long-term aggregate history, viewer-only wallboard authorization and large-fleet soak gates remain open in the authoritative completion plan. Do not equate the owner's report of one healthy deployment, prior-host CI or the full current source suite with these unperformed gates.

## Reference design inputs

WAI-ARIA Window Splitter pattern: https://www.w3.org/WAI/ARIA/apg/patterns/windowsplitter/
MDN pointer capture: https://developer.mozilla.org/en-US/docs/Web/API/Element/setPointerCapture
These guide interaction design; no conformance certification is claimed.
