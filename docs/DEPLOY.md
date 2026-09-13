# Deploy and recovery

## Server

Data directory mode 0700. HTTPS on 8777. Private CA in `$data/tls/ca.crt`. Leaf auto-renews at 1/3 remaining lifetime and is reloaded without copying files to agents (agents already trust the CA).

Backup:

```bash
monik-server backup --output /safe/monik.db
```

Restore only into an empty data dir. The server starts in restore-mode (disruptive jobs paused) until reconciled.

```bash
monik-server restore --data-dir /empty --input /safe/monik.db
```

## Agent

Install next to `monik-service-host`. Enrollment profile is one-use and expires in 10 minutes. Cloning a disk image requires `monik-agent identity reset` (delete state dir, enroll again).

## Updates

1. Sign on an offline/constrained publisher (`monik-release`).
2. Copy only the signed bundle to the controller.
3. Import in UI (catalog).
4. Roll out: one canary per OS/arch, then batches of five.
5. Incomplete switch/probation on service-host restart rolls back to `prev.bin`.

## Rebind

Prepare → verify candidate → arm fallback → activate → retire old endpoint. Updating, rebind activation and trust retirement must not race: one lifecycle job per agent at a time in this revision (queue on the control channel).
