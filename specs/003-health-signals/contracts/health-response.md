# Contract: `GET /health` response body

Extends the existing readiness endpoint. **HTTP status semantics are unchanged**:
`200` when hard dependencies (own Postgres, and NATS when configured) are healthy;
`503` when a hard dependency is down. Collabora reachability never changes the
status code (soft dependency).

## 200 OK — hard deps healthy

Collabora reachability is probed once (≤2s) during the request and reported in the
body.

Collabora reachable:
```json
{
  "status": "ok",
  "collabora": "reachable",
  "collabora_last_success": "2026-06-24T10:15:30Z"
}
```

Collabora unreachable (status still 200 — service stays in rotation):
```json
{
  "status": "ok",
  "collabora": "unreachable",
  "collabora_last_success": "2026-06-24T10:12:01Z"
}
```

Collabora never reached since startup (`collabora_last_success` omitted):
```json
{
  "status": "ok",
  "collabora": "unreachable"
}
```

| Field | Type | Notes |
|---|---|---|
| `status` | string | `"ok"` on the 200 path (unchanged). |
| `collabora` | string | `"reachable"` or `"unreachable"`, from this request's probe. |
| `collabora_last_success` | string (RFC3339) | Last successful probe time; **omitted** when never reached since startup. |

## 503 Service Unavailable — hard dependency down (UNCHANGED)

```json
{ "status": "db_unavailable" }
```
```json
{ "status": "nats_unavailable" }
```

Collabora is **not** probed on the 503 path (short-circuited before the probe), so
`collabora*` fields are absent.

## Backward compatibility

Existing consumers that read only `status` are unaffected (additive fields only).
`/live` is unchanged.
