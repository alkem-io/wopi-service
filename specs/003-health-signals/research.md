# Phase 0 Research: Health signals observability

All decisions below are grounded in the existing code (paths cited) and the
clarified spec. No `NEEDS CLARIFICATION` remain.

## D1 — Probe contacts Collabora but does NOT mutate the discovery cache

**Decision**: `/health` calls a new `DiscoveryService.Probe(ctx)` that invokes
`port.DiscoveryClient.FetchDiscovery(ctx)` purely to determine reachability. On
success/failure it updates only the in-memory reachability state (reachable flag +
last-success time). It does **not** write to the discovery cache (`s.cached`,
`s.cachedAt`) and does **not** use the stale-cache fallback in `refresh()`.

**Rationale**:
- The clarified design is "probe once per `/health`", decoupled from the cache
  refresh the user deliberately moved away from. Keeping the probe side-effect-free
  honors FR-013 ("MUST NOT change existing control flow") — the 12h lazy cache and
  the proof-failure `InvalidateAndRefresh` path keep their current behavior.
- Reachability must reflect *raw* connectivity. `refresh()` masks failures by
  returning stale cache (`discovery_service.go:118-124`); a reachability probe must
  see the real error, so it calls the client directly, not `refresh()`.

**Alternatives considered**:
- *Probe also refreshes the cache on success* — free freshness benefit, but changes
  effective cache TTL from 12h to the health-poll cadence (~10s) and couples two
  concerns; rejected to keep the change minimal and behavior-preserving.

## D2 — 2s probe timeout enforced via context, over the client's 30s timeout

**Decision**: `HealthHandler` builds a dedicated `context.WithTimeout(ctx, 2s)` for
the probe, separate from the existing 5s DB-ping context. The probe passes this
context to `FetchDiscovery`, whose request is created with
`http.NewRequestWithContext` (`discovery_client.go:64`), so the context deadline
cancels the request at 2s regardless of the client's 30s `http.Client.Timeout`.

**Rationale**: Go applies whichever of (context deadline, client timeout) fires
first. The existing client already honors the request context, so no client change
is needed — a 2s context yields a ≤2s probe (FR-014) while leaving the 30s timeout
intact for the lazy discovery-refresh path. Keeps `/health` responsive and preserves
the soft-dependency guarantee when Collabora hangs.

**Alternatives considered**: a second `http.Client` with a 2s timeout — unnecessary;
the context already bounds it and avoids a duplicate client (DRY).

## D3 — Reachability modeled as a 3-state machine to avoid spurious transitions

**Decision**: Track `unknown → up → down` (an `int` enum guarded by a small
`sync.Mutex`). The first probe establishes the baseline **silently** (no log).
Thereafter, `up→down` logs one **Warn**, `down→up` logs one **Info**; an unchanged
state logs nothing.

**Rationale**: Reachability state starts unobserved at process start (the startup
discovery prime in `main.go:91` uses the lazy cache path, not the probe). Without a
baseline state, the first successful probe would falsely log a "recovered"
transition. The `unknown` baseline prevents that and satisfies FR-011 ("exactly one
record on each transition") and SC-003 (one lost + one regained per outage).
Starting-down is reported via the `/health` body (edge case) without a log, since a
baseline is not a transition.

**Concurrency**: `/health` can be hit concurrently (orchestrator + humans). The
`FetchDiscovery` call runs outside the lock; only the read-prev/set-new/decide-log
section is under the mutex, so transitions can't double-log or tear. Probe frequency
is low (~per readiness poll), so brief serialization of that section is negligible.

**Alternatives considered**: `atomic.Bool` for reachable + `atomic.Int64` for
last-success — lock-free but cannot atomically "swap-and-detect-transition" across
two fields without a race that risks double-logging; the mutex is simpler and
correct.

## D4 — Outcome classification: typed errors only where they carry real signal

**Decision**:
- **PutFile** — introduce two domain sentinel errors in `wopi_service.go`,
  `ErrLockRepo` and `ErrFileWrite`, and wrap the existing failure sites
  (`check lock: %w` → `wopi_service.go:148`; `write file: %w` →
  `wopi_service.go:158`) so the handler maps them to `outcome=lock_repo_error` /
  `write_failed`; anything else in the default branch → `outcome=internal`.
- **Token issuance** — map `service.ErrNoDiscoveryData` →
  `outcome=discovery_unavailable`; all other default-branch failures →
  `outcome=internal`. Do **not** introduce sentinels to split lookup-vs-store-vs-
  generate; the wrapped error string (`lookup document:` / `store token:` /
  `generate token:`) already differentiates them in the `error` field for debugging.

**Rationale**: For PutFile the three categories have genuinely different on-call
responses (file-service problem vs our DB problem), so the typed errors earn their
keep. For token issuance the sub-causes share the same response (a 5xx genuine
failure) and are already distinguishable via the `error` field — adding sentinels
purely for a finer label would be busywork (Constitution XI). Changing the error
wrapping does not change any status code or branch (FR-013): the default branch still
returns 500.

**Alternatives considered**: string-matching the wrapped messages to derive
outcomes — rejected (fragile, violates intent of typed errors).

## D5 — Severity: genuine failures at Error; reachability-down at Warn

**Decision**: Token-issuance and PutFile genuine failures log at **Error** (they are
5xx outcomes per US1/US2). Collabora reachability transitions log at **Warn** (lost)
/ **Info** (regained), since Collabora is a soft dependency and the body field is the
steady-state signal.

**Rationale**: Matches the spec (US1/US2 "error-level record"; FR-011 "warning … /
informational"). `outcome=discovery_unavailable` on token issuance is logged at
Error per US2 even though it stems from the same outage the reachability Warn
reports — the two answer different questions ("are users blocked from opening docs?"
vs "is Collabora down?"), and alert routing/rate-thresholds (consumer-side) decide
what pages. Documented so operators can route `discovery_unavailable` differently if
desired.

## D6 — Cross-package field constants in `internal/obs`

**Decision**: Add `internal/obs/signals.go` exporting the canonical Zap field **keys**
(`event`, `outcome`, `documentId`, `actorId`) and **event names**
(`token_issuance`, `putfile`, `collabora_reachability`). Outcome string constants
live beside their single use site. The `error` field uses Zap's conventional
`zap.Error` (key `error`).

**Rationale**: The field keys/event names are constants shared across the `http` and
`service` packages and are the contract alerts depend on — Constitution VIII requires
constants used in multiple places to have one canonical location. Outcomes are
single-site, so centralizing them too would be premature.

## D7 — No configuration, no dependency, no migration

**Decision**: No new env var (`WOPI_DISCOVERY_REFRESH_INTERVAL` is dropped with the
ticker), no new Go module, no DB migration. The 2s probe timeout is a code constant.

**Rationale**: FR-013 + Constitution XIV (no new deps) and X (no dead config). A 2s
probe timeout has no reason to be operator-tunable; promoting it to config would be
speculative (Constitution XI). It can become config later if a real need appears.
