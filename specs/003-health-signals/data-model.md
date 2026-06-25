# Phase 1 Data Model: Health signals observability

No persistent data. All "entities" are in-memory state or log-record shapes.

## 1. Collabora reachability state (in-memory, on `DiscoveryService`)

| Field | Type | Meaning |
|---|---|---|
| `reachState` | enum `unknown` \| `up` \| `down` (unexported `int`) | Current observed reachability; starts `unknown` until the first probe. |
| `lastSuccess` | `time.Time` | Time of the most recent successful probe; zero value = never reached since startup. |
| `reachMu` | `sync.Mutex` | Guards the transition-detection + state-write section (separate from the existing cache `mu`). |

**State transitions** (evaluated inside `Probe`, after `FetchDiscovery` returns):

```
            success                         failure
unknown ───────────────▶ up            unknown ───────────────▶ down
  (set baseline,                          (set baseline,
   no log)                                 no log)

down ──────────────────▶ up            up ─────────────────────▶ down
  (log Info "regained")                  (log Warn "lost", incl. error)

up ─── success ───▶ up (no log)        down ── failure ──▶ down (no log)
```

`lastSuccess` is updated only on a successful probe.

## 2. `Probe` method contract (domain `DiscoveryService`)

```go
// Probe contacts Collabora once to determine reachability, updates in-memory
// reachability state, logs on transition, and returns the current view.
// It does NOT touch the discovery cache.
func (s *DiscoveryService) Probe(ctx context.Context) (reachable bool, lastSuccess time.Time)
```

- Calls `s.client.FetchDiscovery(ctx)` directly (not `refresh`, to bypass the
  stale-cache fallback).
- `reachable = (err == nil)`.
- Caller (`HealthHandler`) supplies a ≤2s context.

The `HealthHandler` depends on a local consumer-defined interface (not the concrete
service) for testability:

```go
type collaboraProber interface {
    Probe(ctx context.Context) (reachable bool, lastSuccess time.Time)
}
```

## 3. Health-signal log record (Zap structured entry)

One canonical field convention across all three signals (keys centralized in
`internal/obs`):

| Key | Source const | Present for | Notes |
|---|---|---|---|
| `event` | `obs.FieldEvent` | all | one of `obs.EventTokenIssuance`, `obs.EventPutFile`, `obs.EventCollaboraReachability` |
| `outcome` | `obs.FieldOutcome` | token, putfile | category enum (see contracts/log-signals.md) |
| `documentId` | `obs.FieldDocumentID` | token, putfile | `req.DocumentID` (token) / `token.FileID` (putfile) |
| `actorId` | `obs.FieldActorID` | token | issuing actor |
| `error` | `zap.Error` (key `error`) | token, putfile, reachability-lost | wrapped error chain; never contains secrets |

Reachability records carry `event` + `error` (on lost) only — no `outcome`/IDs.

## 4. New domain sentinel errors

**PutFile** (`internal/domain/service/wopi_service.go`):

| Error | Wraps the failure at | Maps to outcome | Status (unchanged) |
|---|---|---|---|
| `ErrLockRepo` | `lockRepo.FindByFileID` failure (`wopi_service.go:148`) | `lock_repo_error` | 500 |
| `ErrFileWrite` | `fileSvc.WriteFile` failure (`wopi_service.go:158`) | `write_failed` | 500 |

**Token issuance** (`internal/domain/service/token_service.go`, plus one in
`discovery_service.go`):

| Error | Wraps the failure at | Maps to outcome | Status (unchanged) |
|---|---|---|---|
| `ErrDocumentLookup` (new) | `fileSvc.FindByID` failure — `lookup document: %w` (`token_service.go:69`) | `metadata_lookup_failed` | 500 |
| `ErrTokenPersist` (new) | `tokenRepo.Create` failure — `store token: %w` (`token_service.go:125`) | `token_persist_failed` | 500 |
| `ErrDiscoveryFetch` (new, in `discovery_service.go`) | cold discovery-fetch failure in `refresh` — `fetch discovery: %w` (`discovery_service.go:126`) | `discovery_unavailable` | 500 |
| `ErrNoDiscoveryData` (existing) | nil discovery svc / empty cache (`token_service.go:140`, `discovery_service.go:73`) | `discovery_unavailable` | 503 |

Wrapped with multi-`%w` so both the sentinel and the underlying error remain
unwrappable, e.g. `fmt.Errorf("write file: %w: %w", ErrFileWrite, err)`. **Status-code
mapping in the handlers is unchanged** (FR-013): the new token sentinels are detected
*inside* the existing `default`/500 branch for outcome labeling only;
`ErrNoDiscoveryData` keeps its 503 branch. Anything not matching a sentinel →
`outcome=internal` (still 500).

## 5. Health response body (extended)

```go
type healthResponse struct {
    Status               string `json:"status"`
    Collabora            string `json:"collabora,omitempty"`              // "reachable" | "unreachable"
    CollaboraLastSuccess string `json:"collabora_last_success,omitempty"` // RFC3339; omitted if never reached
}
func (r healthResponse) Render(w http.ResponseWriter, statusCode int)
```

`Collabora*` fields are populated only on the `200` path (after hard deps pass).
The `503` hard-dependency responses keep `Status` only (fields omitted via
`omitempty`).
