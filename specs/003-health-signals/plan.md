# Implementation Plan: Health signals observability

**Branch**: `003-health-signals` | **Date**: 2026-06-24 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/003-health-signals/spec.md`

## Summary

Make three failure classes observable with **no new dependencies**, **no status-code
or control-flow changes** (the only externally visible change is an added field in
the `/health` body):

1. **Save/PutFile failures** — emit one structured Zap error record at the existing
   PutFile error chokepoint for genuine failures (`write_failed`, `lock_repo_error`,
   `internal`); never for lock conflicts (409) or authorization denials (403).
2. **Token issuance failures** — emit one structured Zap error record at the
   existing token-handler error switch for genuine failures (`discovery_unavailable`,
   `internal`); never for client rejections (404/403/422/400).
3. **Collabora reachability** — `/health` probes Collabora once per request (short
   2s timeout, independent of the 30s discovery client), records reachable state +
   last-success time, reports them in the body without affecting HTTP status (soft
   dependency), and logs one record on each up/down transition.

The three signals share a uniform Zap field convention (`event`, `outcome`,
`documentId`, `actorId`, `error`) whose canonical key/name constants live in a new
`internal/obs` package, so a single alert expression can select genuine failures.

## Technical Context

**Language/Version**: Go 1.26 (existing codebase)
**Primary Dependencies**: existing only — chi v5, Zap, pgx v5, nats.go. **No new runtime dependency** (FR-013, Constitution XIV).
**Storage**: None added — no schema changes, no migrations, no sqlc changes.
**Testing**: Go `testing`; `go.uber.org/zap/zaptest/observer` for asserting log records; `net/http/httptest` for handler tests; fakes for the prober and ports.
**Target Platform**: Linux server (Kubernetes pod; `/health` is the readiness probe).
**Project Type**: Single Go module, hexagonal (web-service).
**Performance Goals**: No added latency on token/PutFile happy paths (records emit only on failure). `/health` adds at most one ~2s-bounded Collabora probe per call.
**Constraints**: No new deps; no change to existing HTTP status codes or control flow except the added `/health` body field; Collabora probe MUST be bounded to ~2s (FR-014).
**Scale/Scope**: Low — observability only; ~7 source files touched + 4 test files.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|---|---|---|
| I. Hexagonal Architecture | ✅ | Reachability state + `Probe` live on the domain `DiscoveryService`; `HealthHandler` (inbound adapter) depends on a small consumer-defined prober interface, not a concrete adapter. The probe reuses the already-injected `port.DiscoveryClient`. No adapter→adapter import. |
| II. WOPI Protocol Compliance | ✅ | No protocol behavior changes; PutFile/token responses and status codes unchanged. |
| III. Alkemio Integration First | ✅ | No change to auth/storage delegation. |
| IV. Type-Safe Database Access | ✅ | No DB access added. |
| V. Security by Design | ✅ | Only document/actor IDs and wrapped operational errors are logged — no secrets/tokens (already the case today; verified the error chains carry no credentials). |
| VI. Test-First Development | ✅ | Each slice writes failing tests first (Phase 1 contracts → tests → impl). |
| VII. Root Cause Analysis | ✅ (N/A) | Net-new feature, not a bug fix. |
| VIII. DRY — Single Source of Truth | ✅ | Field-key and event-name constants centralized in `internal/obs`; outcome strings live next to their (single) use site. |
| IX. Lint on Completion | ✅ | `golangci-lint run` before commit. |
| X. No Legacy Code | ✅ | Dropped the abandoned `WOPI_DISCOVERY_REFRESH_INTERVAL`/ticker design entirely; no compat shims. |
| XI. No Busywork | ✅ | No speculative abstraction; `internal/obs` exists only to satisfy the canonical-constant rule for the cross-package log contract. |
| XII. Meaningful Tests Only | ✅ | Tests assert the FR invariants (record emitted ⇔ genuine failure; transition logged once; status unaffected by Collabora). |
| XIII. Meaningful Success Criteria | ✅ | SC-001..005 are all testable within this service. |
| XIV. Latest Dependencies Always | ✅ | No dependency added. |
| XV. No Assumptions | ✅ | Probe model + timeout resolved via `/speckit.clarify`. |
| Anti-pattern #11 (typed response + `Render`) | ✅ | `healthResponse` stays a named struct with JSON tags; a `Render` method is added and the inline encodes refactored onto it. |

**Result: PASS. No violations → Complexity Tracking section omitted.**

## Project Structure

### Documentation (this feature)

```text
specs/003-health-signals/
├── plan.md              # This file
├── spec.md              # Feature spec (+ Clarifications)
├── research.md          # Phase 0 — decisions
├── data-model.md        # Phase 1 — in-memory state, log record, error types
├── quickstart.md        # Phase 1 — manual verification
├── contracts/
│   ├── health-response.md   # /health JSON body contract
│   └── log-signals.md       # structured log field/outcome contract (alerting)
└── checklists/
    └── requirements.md      # spec quality checklist (from /speckit.specify)
```

### Source Code (repository root)

```text
internal/
├── obs/
│   └── signals.go                         # NEW — canonical log field keys + event names (SoT)
├── domain/
│   └── service/
│       ├── discovery_service.go           # EDIT — reachability state + Probe(ctx) + transition logging
│       ├── discovery_service_test.go      # NEW/EDIT — probe transitions, lastSuccess, baseline
│       └── wopi_service.go                # EDIT — ErrLockRepo / ErrFileWrite sentinels on PutFile wraps
└── adapter/
    └── inbound/
        └── http/
            ├── health_handler.go          # EDIT — prober dep, 2s probe, body fields, Render
            ├── health_handler_test.go     # NEW/EDIT — body field, 200 when Collabora down, 503 hard dep
            ├── token_handler.go           # EDIT — structured failure logs (genuine only)
            ├── token_handler_test.go      # NEW/EDIT — log ⇔ genuine; no log for 404/403/422
            ├── wopi_handler.go            # EDIT — structured PutFile failure logs (genuine only)
            └── wopi_handler_test.go       # NEW/EDIT — log ⇔ genuine; no log for 409/403
cmd/server/main.go                         # EDIT — wire DiscoveryService into NewHealthHandler
```

**Structure Decision**: Existing single-module hexagonal layout is kept as-is. The
only structural addition is `internal/obs` to hold the cross-package log-field
constants (Constitution VIII canonical-location rule). All other changes are edits
at the chokepoints already identified in the spec.

## Phase 0 — Research

See [research.md](./research.md). Resolves: probe-vs-cache interaction, how the 2s
timeout is enforced over the 30s client, reachability baseline/transition modeling,
outcome-classification granularity (and where typed errors are justified vs coarse
`internal`), and log severity per outcome. No `NEEDS CLARIFICATION` remain.

## Phase 1 — Design & Contracts

- [data-model.md](./data-model.md) — reachability state machine (`unknown→up/down`),
  the health-signal log record shape, and the two new domain sentinel errors.
- [contracts/health-response.md](./contracts/health-response.md) — the `/health`
  body JSON contract (200 with `collabora` + `collabora_last_success`; unchanged 503
  shapes for hard deps).
- [contracts/log-signals.md](./contracts/log-signals.md) — the structured-log
  contract operators/alerts depend on: event names, outcome enums per signal, and
  severity.
- [quickstart.md](./quickstart.md) — manual verification steps.

Agent context refreshed via `.specify/scripts/bash/update-agent-context.sh claude`.
