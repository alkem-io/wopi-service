# Quickstart: Verifying health signals

Manual verification of the three signals. Assumes the service is running locally
with structured (JSON) Zap output.

## 1. Collabora reachability in `/health`

**Collabora up:**
```bash
curl -s localhost:8080/health | jq
# → {"status":"ok","collabora":"reachable","collabora_last_success":"...Z"}
```

**Collabora down** (stop Collabora, or point `WOPI_COLLABORA_URL` at a dead port):
```bash
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/health
# → 200   (status stays 200 — soft dependency)
curl -s localhost:8080/health | jq '.collabora'
# → "unreachable"
```

**Transition logs** — watch the log while toggling Collabora:
```bash
# one line when it goes down:
#   level=warn  event=collabora_reachability  error=...
# one line when it comes back:
#   level=info  event=collabora_reachability
# no line is emitted on repeated polls while the state is unchanged
```

**Hard-dependency check still fails the probe** — stop Postgres:
```bash
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/health   # → 503
curl -s localhost:8080/health | jq '.status'                     # → "db_unavailable"
```

## 2. Token issuance failures

```bash
# Genuine failures — one error record per genuine path, with its outcome:
#   Collabora discovery down (cold, no cache) → HTTP 500
#     level=error event=token_issuance outcome=discovery_unavailable documentId=... actorId=...
#   Collabora discovery down (empty cache / nil svc) → HTTP 503
#     level=error event=token_issuance outcome=discovery_unavailable documentId=... actorId=...
#   Alkemio metadata lookup fails (Alkemio DB / file-service meta down) → HTTP 500
#     level=error event=token_issuance outcome=metadata_lookup_failed documentId=... actorId=...
#   Token persistence fails (own DB write) → HTTP 500
#     level=error event=token_issuance outcome=token_persist_failed documentId=... actorId=...
#   Any other internal error → HTTP 500, outcome=internal
#
# Expected client rejection — NO signal record (only the access-log line):
#   request a document the actor cannot access → HTTP 403, and grep shows
#   NO `event=token_issuance` record.
```

Verify quietness of client rejections:
```bash
# After issuing a request that 404s/403s/422s, this should print nothing:
grep 'event":"token_issuance' <logfile>   # (only genuine failures appear)
```

## 3. Save / PutFile failures

```bash
# Genuine failure — file-service write rejected:
#   level=error event=putfile outcome=write_failed documentId=...
#
# Lock conflict (two editors) — NO signal record:
#   HTTP 409 returned to Collabora, but grep shows NO `event=putfile` record.
```

## 4. Automated tests

```bash
go test ./internal/domain/service/... ./internal/adapter/inbound/http/...
golangci-lint run
```

Key assertions:
- `discovery_service_test.go` — probe sets reachable/lastSuccess; `up→down` logs one
  Warn, `down→up` one Info; first observation logs nothing; unchanged state silent.
- `health_handler_test.go` — body carries `collabora`; status is 200 when Collabora
  is down but Postgres/NATS up; 503 only when a hard dep is down.
- `token_handler_test.go` — error record on genuine failures; none on 404/403/422.
- `wopi_handler_test.go` — error record on `write_failed`/`lock_repo_error`; none on
  409/403.
