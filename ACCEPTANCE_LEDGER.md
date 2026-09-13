# Acceptance ledger (first source revision)

Status values: PASS / FAIL / NOT RUN. Native Windows boot and public-IP deployment are NOT RUN.

| Group | Result | Evidence |
|---|---|---|
| Unit: password argon2id | PASS | `go test ./internal/secure` |
| Unit: SSRF/local probe policy | PASS | `go test ./internal/netutil` |
| Unit: rules/freshness | PASS | `go test ./internal/rules` |
| Unit: action registry required IDs | PASS | `go test ./internal/actions` |
| Unit: sqlite idempotency + enrollment consume-once | PASS | `go test ./internal/storage` |
| Unit: TUF sign/verify/pack/import | PASS | `go test ./internal/tufutil` |
| Unit: update stage/activate/rollback | PASS | `go test ./internal/update` |
| Integration: login, CSRF, enroll, report, overview | PASS | `go test ./internal/server` |
| Integration: idempotent backup.submit | PASS | `go test ./internal/server` |
| UI screens loading/empty | PASS | Vue SPA routes + empty states |
| Linux amd64 build | PASS | `make linux` |
| Windows amd64 cross-compile | PASS | `make windows` (compile only) |
| LAN HTTPS listen 0.0.0.0:8777 | PASS | local deploy on 192.168.12.128 |
| Native Windows service boot | NOT RUN | no Windows host in this environment |
| Crash at update boundaries on Windows | NOT RUN | |
| 24h soak | NOT RUN | |
| Playwright browser matrix | NOT RUN | focused HTTP tests used instead |
| Scan/deploy 46.120.103.61 | NOT RUN | forbidden as live target |
