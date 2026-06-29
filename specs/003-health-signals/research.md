# Phase 0 Research: Health signals observability

All decisions below are grounded in the existing code (paths cited) and the
clarified spec. No `NEEDS CLARIFICATION` remain.

## D1 — Probe contacts Collabora but does NOT mutate the discovery cache

**Decision**: `/health` calls a new `DiscoveryService.Probe(ctx)` that invokes
`port.DiscoveryClient.FetchDiscovery(ctx)` purely to determine reachability. On
success/failure it updates only the in-memory reachability state (reachable flag +
last-success time). It does **not** write to the discovery cache (`s.cached`,
`s.cachedAt`) and does **not** use the stale-cache fallback in `refresh()`.

**Reachable definition (clarified)**: a probe counts as reachable only when the
response is **2xx AND its body parses as `wopi-discovery` XML**. This is already
exactly what `FetchDiscovery` returns `err == nil` for: it rejects non-200
(`discovery_client.go:75-77`) and `xml.Unmarshal`s into a struct whose
`XMLName xml.Name \`xml:"wopi-discovery"\`` makes a non-discovery body (e.g. a
reverse-proxy placeholder page served while coolwsd warms up) fail with a root-element
mismatch (`discovery_client.go:84-87`). So `reachable = (FetchDiscovery err == nil)`
satisfies the clarified definition with **no new parsing code** — it closes the
"2xx-but-not-actually-serving-WOPI" false-positive gap for free.

**Bounded body read (clarified)**: the probe must read the body under a bounded
reader so a hung/huge body can't blow the probe budget. `FetchDiscovery` currently
uses unbounded `io.ReadAll` (`discovery_client.go:79`); change it to
`io.ReadAll(io.LimitReader(resp.Body, maxDiscoveryBytes))` with a generous cap (1 MiB
— discovery XML is a few KB). This is a defensive bound shared by both the probe and
the lazy-cache fetch; it does not change control flow or status codes (a legitimate
discovery doc is far under the cap).

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
- *Status-only reachable (2xx, no body check)* — rejected per clarification: a
  reverse-proxy can return 200 for a placeholder while coolwsd is still spawning kit
  processes, false-positiving on exactly the warm-up window operators care about.

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

**Per-instance scope (clarified)**: transition state lives in process memory on each
replica; there is no cross-replica coordination (would require a shared store,
violating FR-013). "Exactly one record per transition" is therefore scoped per
service instance — a single Collabora outage may produce up to one "lost" record per
running replica, and state resets to `unknown` on restart. This is the only option
consistent with the no-new-dependency constraint.

**Concurrency**: `/health` can be hit concurrently *within an instance* (liveness +
readiness probes, plus humans). The `FetchDiscovery` call runs outside the lock; only
the read-prev/set-new/decide-log section is under the mutex, so concurrent probes
can't double-log or tear a transition. This serialization is what makes the
per-instance "exactly one" guarantee hold under overlapping probes. Probe frequency
is low (~per readiness poll), so brief serialization of that section is negligible.

**Alternatives considered**: `atomic.Bool` for reachable + `atomic.Int64` for
last-success — lock-free but cannot atomically "swap-and-detect-transition" across
two fields without a race that risks double-logging; the mutex is simpler and
correct.

## D4 — Outcome classification via typed sentinels (status codes untouched)

**Decision**:
- **PutFile** — introduce two domain sentinel errors in `wopi_service.go`,
  `ErrLockRepo` and `ErrFileWrite`, and wrap the existing failure sites
  (`check lock: %w` → `wopi_service.go:148`; `write file: %w` →
  `wopi_service.go:158`) so the handler maps them to `outcome=lock_repo_error` /
  `write_failed`; anything else in the default branch → `outcome=internal`.
- **Token issuance** — the clarified spec (FR-006) fixes a **four-category** outcome
  set: `metadata_lookup_failed`, `discovery_unavailable`, `token_persist_failed`,
  `internal`. The handler classifies each genuine failure into exactly one via
  `errors.Is`, using sentinels that wrap the real failure sites in `token_service.go`:

  | Outcome | Sentinel | Wrap site |
  |---|---|---|
  | `metadata_lookup_failed` | `ErrDocumentLookup` (new) | `lookup document: %w` (`token_service.go:69`) |
  | `discovery_unavailable` | `ErrNoDiscoveryData` (existing) **and** `ErrDiscoveryFetch` (new) | `FindActionByExtension`/nil-svc (`token_service.go:140,73`) **and** the cold discovery-fetch failure in `refresh` (`discovery_service.go:126`) |
  | `token_persist_failed` | `ErrTokenPersist` (new) | `store token: %w` (`token_service.go:125`) |
  | `internal` | none (default) | check-privilege, generate-token, build-editor-URL, and any other genuine 500 |

**Status codes stay identical (FR-013)**. Classification is layered *separately* from
status mapping:
- `ErrNoDiscoveryData` keeps its existing 503 branch and now also logs
  `outcome=discovery_unavailable`.
- `ErrDiscoveryFetch`, `ErrDocumentLookup`, `ErrTokenPersist` are all caught **inside
  the existing `default`/500 branch** for outcome labeling only — they do not move any
  request to a new status code. In particular, a cold-start Collabora outage
  (`refresh` returns `fetch discovery: %w`, not `ErrNoDiscoveryData`) stays **500** as
  today but is now correctly labeled `discovery_unavailable` instead of `internal`.

**Rationale**: The clarification makes the finer token taxonomy a requirement, not a
nicety — SC-005 needs a single alert expression to select genuine failures *by class*
(e.g. distinguish a metadata-DB outage from a Collabora outage from a token-store
outage), each of which has a different on-call response. With the categories mandated,
typed sentinels are the correct mechanism (Constitution VIII/XI) and are no longer
busywork. Sentinels — not string-matching the wrapped messages — keep classification
robust to message edits. Note: when Collabora is down but a **stale discovery cache**
exists, `refresh` returns the stale data with no error (`discovery_service.go:122-124`)
and token issuance **succeeds** — correctly producing *no* failure record (matches
SC-004 graceful degradation).

**Alternatives considered**:
- *Keep only `discovery_unavailable` + `internal` for token issuance* (the pre-
  clarification design) — rejected: FR-006 now enumerates four categories, and
  collapsing lookup/persist into `internal` defeats per-class alerting (SC-005).
- *String-matching the wrapped messages to derive outcomes* — rejected (fragile,
  breaks on message edits, violates the intent of typed errors).

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
