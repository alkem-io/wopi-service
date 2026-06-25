# Tasks: Health signals observability

**Input**: Design documents from `/specs/003-health-signals/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: INCLUDED — the constitution mandates Test-First Development (Principle VI)
and the plan specifies each slice writes failing tests first. Test tasks assert FR
invariants only (Principle XII — no coverage padding).

**Organization**: Tasks are grouped by user story (US1=P1, US2=P2, US3=P3) so each
story is an independently testable increment.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1 / US2 / US3 (Setup, Foundational, Polish phases carry no story label)
- All paths are repository-relative.

## Path Conventions

Single Go module, hexagonal layout. Source under `internal/` and `cmd/`; tests are
Go `_test.go` files colocated with the package under test.

---

## Phase 1: Setup (Shared)

**Purpose**: Establish a known-green baseline before any change.

- [x] T001 Confirm baseline is green before changes: run `go build ./...`, `go test ./...`, and `golangci-lint run` from repo root; record that all pass.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The shared structured-log field convention that all three signals depend on.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [x] T002 Create `internal/obs/signals.go` exporting the canonical Zap field-key constants (`FieldEvent="event"`, `FieldOutcome="outcome"`, `FieldDocumentID="documentId"`, `FieldActorID="actorId"`) and event-name constants (`EventTokenIssuance="token_issuance"`, `EventPutFile="putfile"`, `EventCollaboraReachability="collabora_reachability"`) per data-model.md §3 and contracts/log-signals.md (Constitution VIII — single source of truth). No test task: these are bare constants (Principle XII).

**Checkpoint**: `internal/obs` available → user stories can begin.

---

## Phase 3: User Story 1 — See when documents fail to save (Priority: P1) 🎯 MVP

**Goal**: Emit exactly one structured error record per genuine PutFile failure
(`write_failed`, `lock_repo_error`, `internal`), and none for lock conflicts (409) or
authorization denials (403).

**Independent Test**: Force the file-service write to fail (and the lock store to
error) and confirm one `event=putfile` record with the correct `outcome` and the
document id; confirm a 409 lock conflict and a 403 produce no record.

### Tests for User Story 1 ⚠️ (write first, must fail)

- [x] T003 [P] [US1] In `internal/domain/service/wopi_service_test.go`, add tests asserting that the PutFile lock-store failure path wraps `ErrLockRepo` and the file-write failure path wraps `ErrFileWrite` (both detectable via `errors.Is` through the returned chain), and that the underlying error remains unwrappable.
- [x] T004 [P] [US1] In `internal/adapter/inbound/http/wopi_handler_test.go`, add `zaptest/observer` tests asserting: write failure → one `error` record `event=putfile`, `outcome=write_failed`, with `documentId`; lock-store error → `outcome=lock_repo_error`; uncategorized 500 → `outcome=internal`; and that a 409 lock conflict and a 403 denial emit **zero** records and leave the HTTP status unchanged.

### Implementation for User Story 1

- [x] T005 [US1] In `internal/domain/service/wopi_service.go`, declare sentinels `ErrLockRepo` and `ErrFileWrite` and wrap the existing failure sites with multi-`%w` (`check lock` at ~line 148 → `ErrLockRepo`; `write file` at ~line 158 → `ErrFileWrite`). Do not change any return path or status mapping (FR-013). (Makes T003 pass.)
- [x] T006 [US1] In `internal/adapter/inbound/http/wopi_handler.go`, at the PutFile error chokepoint classify genuine failures via `errors.Is` and emit one `h.logger.Error` record using `internal/obs` field keys + `EventPutFile`, `outcome` ∈ {`write_failed`,`lock_repo_error`,`internal`}, `documentId`, `zap.Error`; skip emission for lock-conflict (409) and authorization (403/404) outcomes. (Makes T004 pass.)

**Checkpoint**: US1 fully functional and independently testable — MVP deliverable.

---

## Phase 4: User Story 2 — See when editor tokens fail to issue (Priority: P2)

**Goal**: Emit one structured error record per genuine token-issuance failure,
classified into the four mandated outcomes (`metadata_lookup_failed`,
`discovery_unavailable`, `token_persist_failed`, `internal`), and none for client
rejections (404/403/422/400). HTTP status codes unchanged (FR-013).

**Independent Test**: Drive each genuine failure path (metadata lookup, cold
discovery fetch, token persistence, other internal) and confirm one
`event=token_issuance` record with the correct `outcome`, `documentId`, `actorId`;
confirm not-found/forbidden/unsupported/bad-request emit none.

### Tests for User Story 2 ⚠️ (write first, must fail)

- [x] T007 [P] [US2] In `internal/domain/service/token_service_test.go`, add tests asserting `IssueToken` wraps `ErrDocumentLookup` on the document-lookup failure (~line 69) and `ErrTokenPersist` on the token-store failure (~line 125), both detectable via `errors.Is`, underlying error preserved.
- [x] T008 [P] [US2] In `internal/domain/service/discovery_service_test.go`, add a test asserting that a cold discovery fetch failure (no prior cache) returns an error matching `ErrDiscoveryFetch` via `errors.Is`, while the stale-cache fallback path still returns the cached data with no error (no failure produced).
- [x] T009 [P] [US2] In `internal/adapter/inbound/http/token_handler_test.go`, add `zaptest/observer` tests asserting each genuine path emits one `error` record `event=token_issuance` with the correct `outcome` (`metadata_lookup_failed` / `discovery_unavailable` for both `ErrNoDiscoveryData`(503) and `ErrDiscoveryFetch`(500) / `token_persist_failed` / `internal`), carrying `documentId` and `actorId`; and that 404/403/422/400 client rejections emit **zero** records with their status codes unchanged. **FR-013 status pin**: assert the relabeled cold-outage path keeps HTTP **500** (`ErrDiscoveryFetch`) and the empty-cache path keeps HTTP **503** (`ErrNoDiscoveryData`) even though both share `outcome=discovery_unavailable` — outcome labeling must not shift any status code.

### Implementation for User Story 2

- [x] T010 [US2] In `internal/domain/service/token_service.go`, declare sentinels `ErrDocumentLookup` and `ErrTokenPersist` and wrap the `lookup document` (~line 69) and `store token` (~line 125) sites with multi-`%w`; no control-flow/status change. (Makes T007 pass.)
- [x] T011 [US2] In `internal/domain/service/discovery_service.go`, declare sentinel `ErrDiscoveryFetch` and wrap the cold-fetch failure in `refresh` (~line 126, `fetch discovery: %w`) so a genuine Collabora outage during issuance is identifiable; keep the stale-cache fallback and all returns unchanged. (Makes T008 pass.) ⚠️ Same file as T017 — sequence before T017.
- [x] T012 [US2] In `internal/adapter/inbound/http/token_handler.go`, in the issuance error switch keep status mapping identical (FR-013) and add outcome classification: the `ErrNoDiscoveryData`/503 branch logs `outcome=discovery_unavailable`; inside the `default`/500 branch use `errors.Is` to label `ErrDiscoveryFetch`→`discovery_unavailable`, `ErrDocumentLookup`→`metadata_lookup_failed`, `ErrTokenPersist`→`token_persist_failed`, else `internal`. Emit one `h.logger.Error` record with `internal/obs` keys + `EventTokenIssuance`, `outcome`, `documentId`, `actorId`, `zap.Error`. Do not emit on the 404/403/422/400 branches. (Makes T009 pass.)

**Checkpoint**: US1 and US2 both work independently.

---

## Phase 5: User Story 3 — Know whether Collabora is reachable (Priority: P3)

**Goal**: `/health` probes Collabora once per request (≤2s, bounded body read),
reports reachable state + last-success time in the body without changing HTTP status
(soft dependency), and logs one record per up/down transition (per-instance,
in-memory). Hard-dependency 503 behavior unchanged.

**Independent Test**: Make Collabora unreachable and confirm `/health` reports
`collabora=unreachable` while returning 200; confirm exactly one warn on the down
transition and one info on recovery regardless of outage length; confirm a 200
response whose body is non-discovery XML counts as unreachable; confirm a hard-dep
outage still yields 503.

### Tests for User Story 3 ⚠️ (write first, must fail)

- [x] T013 [P] [US3] In `internal/adapter/outbound/collabora/discovery_client_test.go`, add tests asserting `FetchDiscovery` treats a non-2xx response and a 2xx-with-non-`wopi-discovery`-body (placeholder page) as errors (→ unreachable), and that an oversized body is bounded by the `LimitReader` cap.
- [x] T014 [P] [US3] In `internal/domain/service/discovery_service_test.go`, add tests for `Probe`: first probe sets baseline silently (no log) for both up and down; `up→down` logs exactly one Warn (with error) and `down→up` exactly one Info; unchanged state logs nothing; `lastSuccess` updates only on success; concurrent probes during a transition log at most once (mutex). ⚠️ Same file as T008 — sequence after T008.
- [x] T015 [P] [US3] In `internal/adapter/inbound/http/health_handler_test.go`, add tests asserting: 200 path includes `collabora` (`reachable`/`unreachable`) from this request's probe and `collabora_last_success` (RFC3339), omitted when never reached; Collabora down keeps overall status 200; a hard-dependency (DB/NATS) outage still returns 503 with `collabora*` absent (probe short-circuited). **FR-014 hang guarantee**: with a prober fake that blocks past 2s, `/health` still returns 200 with `collabora=unreachable` and the handler returns within the ~2s probe bound (assert via the request context deadline / a timed call), proving a hung Collabora cannot stall the readiness response.

### Implementation for User Story 3

- [x] T016 [US3] In `internal/adapter/outbound/collabora/discovery_client.go`, replace the unbounded `io.ReadAll(resp.Body)` (~line 79) with `io.ReadAll(io.LimitReader(resp.Body, maxDiscoveryBytes))` using a package const `maxDiscoveryBytes = 1 << 20` (1 MiB); behavior otherwise unchanged. (Makes T013 pass.)
- [x] T017 [US3] In `internal/domain/service/discovery_service.go`, add per-instance reachability state (`reachState` enum `unknown|up|down`, `lastSuccess time.Time`, dedicated `reachMu sync.Mutex`) and a `Probe(ctx) (reachable bool, lastSuccess time.Time)` method that calls `client.FetchDiscovery` directly (not `refresh`, no cache mutation), updates state + `lastSuccess` under the mutex, and logs one transition record (`Warn` on lost incl. `zap.Error`, `Info` on regained) via `internal/obs` keys + `EventCollaboraReachability`. (Makes T014 pass.) ⚠️ Same file as T011 — sequence after T011.
- [x] T018 [US3] In `internal/adapter/inbound/http/health_handler.go`, add a `collaboraProber` consumer interface dependency, build a dedicated `context.WithTimeout(ctx, 2*time.Second)` for the probe on the 200 path only (after hard-dep checks), populate `collabora`/`collabora_last_success` on `healthResponse`, and add a `Render(w, statusCode)` method (anti-pattern #11); 503 hard-dep path unchanged and not probed. (Makes T015 pass.)
- [x] T019 [US3] In `cmd/server/main.go`, wire the existing `DiscoveryService` into `NewHealthHandler` as the `collaboraProber`.

**Checkpoint**: All three user stories independently functional.

---

## Phase 6: Polish & Cross-Cutting

- [x] T020 Regenerate `openapi.yaml` via `make openapi` to reflect the new `/health` response fields (`collabora`, `collabora_last_success`); confirm the stale-check passes.
- [x] T021 [P] Run `golangci-lint run` and resolve any violations introduced (Constitution IX).
- [~] T022 Execute the manual steps in `specs/003-health-signals/quickstart.md` and confirm each signal behaves as documented. **PARTIAL**: the automated portion (quickstart §4) passes; the live `curl` steps (§1–§3) require a running wopi-service + Postgres + Collabora and remain to be run against a deployment.
- [x] T023 Run the full `go test ./...` suite and confirm green (all SC-001..005 invariants covered).

---

## Dependencies & Execution Order

- **Setup (T001)** → **Foundational (T002)** → user stories.
- **T002 blocks all stories** (every signal imports `internal/obs`).
- **US1 (T003–T006)**, **US2 (T007–T012)**, **US3 (T013–T019)** are otherwise independent and can be delivered in priority order. US1 alone is a shippable MVP.
- **Cross-story shared file** — `internal/domain/service/discovery_service.go` (and its `_test.go`) are touched by both US2 and US3:
  - T011 (US2, `ErrDiscoveryFetch`) before T017 (US3, reachability state).
  - T008 (US2 test) before T014 (US3 test).
- Within each story: tests (parallel) → implementation; the handler task depends on its story's service/sentinel tasks.
- **Polish (T020–T023)** after all targeted stories are complete.

## Parallel Execution Examples

- Foundational done → launch all three stories' test-writing in parallel: T003, T004 (US1); T007, T008, T009 (US2); T013, T015 (US3). (T014 waits on T008 — same file.)
- Within US1: T003 and T004 run in parallel (different files), then T005, then T006.
- Within US3: T013 and T015 run in parallel; T016 and T018 are independent until T017/T019 wiring.

## Implementation Strategy

1. **MVP** = Setup + Foundational + **US1** (the highest-stakes, least-visible signal — silent save failures). Ship and validate alerting on `event=putfile` before continuing.
2. Add **US2** (token issuance, four-category outcomes) — independent except for the shared discovery sentinel.
3. Add **US3** (reachability probe + `/health` body) last — diagnostic signal; includes the only externally visible change (additive body fields) and the `openapi.yaml` regen.
