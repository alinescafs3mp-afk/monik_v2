# Monik V14: current inventory and a usable external enrollment profile

Date: 2026-09-15. Reviewed baseline: `b969c6a34d3852687bd3aa3c73212ec4e183f55f`, tree `2808fdc27e4e9da45553f299e81cce037ea6e8ed`.

This is a tested source correction, not a production deployment or a declaration that every release gate is closed. The owner's live controller, credentials and remote servers were not accessed. No GitHub writes were attempted. See the separate evidence summary for actual final command results.

## 1. The owner's two observations

The external controller is `https://46.150.103.61:8777/`. Changing only a compiled default was insufficient: enrollment profiles previously embedded the controller's saved LAN URL, which takes precedence at setup. V14 corrects the shared bootstrap default and exposes a separate, TLS-checked address for each new profile. Existing LAN agent routes and the controller's saved advertised URL are NOT silently changed.

The source explains how a single machine can accumulate services: positive discovery upserts existed, but there was no negative inventory reconciliation. Temporary development/test listeners remained indefinitely. This is a confirmed code defect, not proof of the exact composition of the owner's reported 38 services. PID changes alone did not create duplicates: the existing `(agent_id, dial_target, host_header)` identity was already retained. V14 preserves that rule rather than introducing a second identity scheme or merging unrelated virtual hosts.

## 2. Presence is not health

An additive versioned OS-listener snapshot is now separate from bounded HTTP identification. The agent advertises `listener_inventory_v1` and reports `inventory_version`, `listener_targets` and `listener_coverage_complete`. Protocol schema remains 3; new zero fields use `omitempty`, preserving old-report canonical serialization.

The snapshot covers eligible local listeners in the agent's network namespace under the existing local-probe safety policy. Metadata/link-local destinations remain excluded. It is not a cross-container/namespace scan, virtual-host catalogue or network-wide scanner. Successful socket enumeration is independent of HTTP timeouts, authentication, response codes, paused checks and the HTTP identification budget.

At most 4096 canonical listener addresses are carried. Truncation, enumeration errors and malformed socket entries cannot prove complete absence. IPv4 and IPv6 remain distinct; wildcard bindings normalize to the loopback address in the same family. No DNS lookup is used to reconcile identity.

### State transitions

- A positive socket observation means `present`, even when HTTP probing is disabled or unsuccessful.
- The first complete scan missing an endpoint makes it `unconfirmed`.
- At least two distinct complete scans AND at least 60 seconds since the first miss are required for `missing`. With a 60-second discovery interval this normally takes a few minutes, not an exact guaranteed timer.
- A repeated report or scan cannot count as the second observation. Scans are ordered by their start timestamp; inventories older than five minutes are not used for reconciliation.
- Partial scans can prove presence but cannot increase absence evidence. Offline/backfilled data cannot retire current inventory.
- Rediscovery revives the same row and retains name, history, flags and request. A positive old-agent report can also revive it after a worker rollback; old agents cannot prove absence.

Presence rows and scan checkpoints commit in the SAME transaction as telemetry and dedup receipts. A fault-injected database write rolls them all back. Data is never acknowledged as committed after partial reconciliation.

## 3. Current UI without disappearing outages

A missing listener is removed from the normal UI list only when it is listener-discovered, not pinned for the overview, and not actively monitored. If a saved check was just disabled, configuration application must be confirmed first. Global machine pause does not count as disabling the individual expected service.

Configured/enabled services, explicit overview selections and manually created endpoints remain visible when absent. Otherwise stopping a critical service would perversely remove the problem from the dashboard. Their presence message explicitly states that the port was not found. HTTP failures by themselves NEVER hide a service.

The Services page and the machine's ServiceList offer **Show disappeared** with a count. `/api/v1/services` returns current items by default; `?inventory=all` includes retained inactive discoveries. The machine-detail API still includes retained records for editing/history. Overview counts exclude automatically inactive records. There is no deletion of old measurements, incidents, names or request definitions and no automatic unpinning.

Historical service configuration remains stored. V14 removes visual accumulation, not all retained metadata from SQLite. A later bounded garbage-collection policy must preserve history and owner-defined recovery anchors; it is not silently implemented here. Past open incidents are not fabricated as recovered simply because monitoring was disabled.

## 4. Discovery checks publish one configuration revision

One discovered batch previously called the configuration publisher once per new endpoint. Two new services could increment revision 1 to 3 instead of 2. This unnecessarily invalidated results of unchanged checks and enlarged config history. The batch now produces at most ONE revision, preserves all existing requests/secret bindings/lifecycle flags, validates the complete config, and maintains the default of new checks being disabled.

## 5. External address, TLS and offline SAN extension

The new-profile page defaults to `https://46.150.103.61:8777`, with a separate button to use the saved controller URL. Both automatic-discovery JSON and one-time enrollment YAML use the explicitly selected origin. The profile does not contain a shared fleet credential; only existing public CA material is exposed.

An authenticated GET preflight parses the origin and compares the ACTIVE loaded leaf certificate's validity and SAN with its host. It does not dial the proposed address or claim that NAT/firewall/reverse-proxy routing works. An unsuitable leaf blocks a normal profile download. The UI detects a changed CA while the page is open and requires a fresh review rather than mixing new certificate results with old trust bytes.

A new explicit offline command:

```sh
monik-server tls-add-name --data-dir /ACTUAL/CONTROLLER/STATE --name 46.150.103.61
```

requires existing CA and leaf files and the controller process lock. It refuses a running controller and incomplete identity state. It adds a SAN to a newly issued leaf while retaining the same CA/private key and all old names. It is idempotent and never opens or edits the database. The actual data directory must be used. Do not generate a replacement CA or reset enrolled agents.

Ordinary renewal now UNIONs previous SANs with current names/interfaces, deduplicated. Previously a nonempty new list could drop old names on renewal. Existing atomic leaf-bundle publication remains in use. The refactoring of `hostOf` to URL parsing is defensive cleanup; the trailing-slash regression guard ALSO PASSED on the baseline and is not counted as a discovered bug.

For IP-based TLS, Go verifies IP SANs and ignores legacy CommonName. Source: https://pkg.go.dev/crypto/x509#Certificate.VerifyHostname . Local certificate validation does not establish the certificate actually served by a separate TLS-terminating reverse proxy.

## 6. Collector correctness

The Windows IPv6 OWNER_PID table parser had incorrect offsets/stride. It now decodes the documented 56-byte row: local address offset 0, local scope 16, port 20, state 48 and PID 52. Port and scope are decoded in network order; state/PID/count use native little endian for supported Windows amd64. Buffer length/count are checked without unsafe casts. The native query retries bounded size changes and rejects failures in either address family instead of calling a partial enumeration complete.

IPv4 and IPv6 binary fixtures and a bounded fuzz target cover decoding. This is NOT native Windows API/SCM acceptance. Source: https://learn.microsoft.com/en-us/windows/win32/api/tcpmib/ns-tcpmib-mib_tcp6row_owner_pid .

Linux now rejects malformed TCP-table headers/listening rows rather than silently dropping them. The optional missing IPv6 table is distinguished from failure of the required IPv4 table. Process/PID access limitations still do not remove a successfully read socket.

## 7. Evidence and adversarial cases

Four named compatible tests failed against unchanged baseline behavior, then passed with fixes:
1. Missing unmonitored listener remained in the current list indefinitely.
2. One discovery batch produced multiple desired revisions.
3. Renewal with new nonempty names dropped old TLS SANs.
4. Malformed Linux listening row was silently treated as no listener.

The host parser guard passed on the baseline; do not inflate the negative count. Windows layout corrections are supported by source review and byte fixtures, not a claimed native reproduction.

Additional tests cover duplicate/stale/partial/backfilled snapshots, empty complete inventory, HTTP budget exhaustion, presence without HTTP, database fault rollback, restart durability, owner names/pins, PID and scheme changes, monitored or pinned disappearance, unapplied pause, manual endpoints, same-ID revival, selected profile with a LAN controller, private-key exclusion, CA preservation, repeated SAN addition and locked offline CLI refusal.

A kernel-listener integration test opens and closes a REAL Linux socket and feeds its observed presence through actual temporary SQLite to current/all API views. Only the grace clock is advanced artificially; it is not an elapsed multi-minute native service test. The existing signed-update/failed-candidate recovery tests are re-run separately against compiled Linux processes. See evidence for exact results.

## 8. Boundaries and deployment

Update controller/UI first, then agent and matching service-host. Keep existing identities, credentials, queue/config journals and actual routes. The old controller should not be used to ingest new inventory reports or run an active newer rollout after downgrade. Older agents remain usable with this controller, but absence cannot be inferred until `listener_inventory_v1` is reported.

A reported 38 services will not necessarily all disappear: actively monitored or selected items intentionally remain expected. Disable obsolete checks and unpin obsolete screen items explicitly; the history view remains available. Do not mass-disable a server merely to reduce the number.

Before a first remote Linux pilot: use a trusted profile carrying the external origin; validate TLS from that remote machine; install the supervised pair; retain independent SSH/provider console access; approve enrollment; confirm 5-second telemetry, applied config and capabilities; test system-service restart and boot there. Full protected restore, independent service-host self-update, native Windows installation/boot, long-term aggregation and fleet/soak limits remain open. No new broad agent execution authority or external monitoring dependency was added.
