# Contract: Structured health-signal log records

The alerting contract. Each genuine failure or reachability transition produces one
Zap record with a uniform field set. Field keys and event names are defined once in
`internal/obs/signals.go` (canonical source of truth).

## Common fields

| Key | Always present? | Value |
|---|---|---|
| `event` | yes | `token_issuance` \| `putfile` \| `collabora_reachability` |
| `outcome` | failures only | per-signal enum (below) |
| `documentId` | token, putfile | document/file identifier |
| `actorId` | token only | issuing actor identifier |
| `error` | failures + reachability-lost | wrapped error chain (no secrets) |
| Zap `level` | yes | `error` \| `warn` \| `info` (below) |

> Note: ordinary client rejections and lock conflicts are **not** emitted as signal
> records — they remain visible only in the existing per-request access log
> (`middleware_logger.go`). "A signal record exists ⇒ something is genuinely wrong."

## Signal: `token_issuance`

| Condition | Emitted? | level | outcome |
|---|---|---|---|
| Discovery/Collabora unavailable (`ErrNoDiscoveryData`, 503) | ✅ | error | `discovery_unavailable` |
| Other internal failure (lookup/store/generate/editor-URL, 500) | ✅ | error | `internal` |
| Document not found (404) | ❌ | — | — |
| Not authorized (403) | ❌ | — | — |
| Unsupported MIME / extension (422) | ❌ | — | — |
| Bad request / missing documentId (400) | ❌ | — | — |

Fields: `event=token_issuance`, `outcome`, `documentId`, `actorId`, `error`.

## Signal: `putfile`

| Condition | Emitted? | level | outcome |
|---|---|---|---|
| file-service write failure (`ErrFileWrite`, 500) | ✅ | error | `write_failed` |
| lock-store query failure (`ErrLockRepo`, 500) | ✅ | error | `lock_repo_error` |
| Other internal failure (500) | ✅ | error | `internal` |
| Lock conflict (409) | ❌ | — | — |
| Not authorized (403) | ❌ | — | — |
| Document not found (404) | ❌ | — | — |

Fields: `event=putfile`, `outcome`, `documentId`, `error`.

## Signal: `collabora_reachability`

| Transition | Emitted? | level |
|---|---|---|
| `up → down` | ✅ (once) | warn |
| `down → up` | ✅ (once) | info |
| first observation (baseline) | ❌ | — |
| unchanged state (still up / still down) | ❌ | — |

Fields: `event=collabora_reachability`, `error` (on the down transition only).

## Example alert expressions (illustrative)

```
level="error" AND event="token_issuance"                # genuine token failures
level="error" AND event="putfile"                       # genuine save failures
level="error" AND event="putfile" AND outcome="write_failed"   # file-service problems
level="warn"  AND event="collabora_reachability"        # Collabora went down
```
