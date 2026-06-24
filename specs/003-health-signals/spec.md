# Feature Specification: Health signals observability

**Feature Branch**: `003-health-signals`
**Created**: 2026-06-24
**Status**: Draft
**Input**: User description: "Health signals observability for wopi-service. Track three failure classes via structured logs and an extended /health endpoint, with no new dependencies (no Prometheus/metrics): WOPI token issuance failures, Collabora reachability, and save/PutFile errors."

## User Scenarios & Testing *(mandatory)*

The "users" of this feature are **platform operators / on-call engineers** who
run the wopi-service in production and need to know, without reading source or
attaching a debugger, when the service is failing its three core jobs: issuing
editor tokens, reaching Collabora, and saving documents.

### User Story 1 - See when documents fail to save (Priority: P1)

An on-call engineer needs to know the moment the service starts failing to write
edited documents back through the file-service, because a failed save is silent
to the operator today (it surfaces only as an HTTP status to Collabora) and risks
users losing work.

**Why this priority**: A failing save is the highest-stakes failure — it can mean
lost user edits — and is currently the least visible. Making it observable is the
single most valuable signal.

**Independent Test**: Force the file-service write path to fail (or the lock
store to error) and confirm a single, structured, alertable failure record is
produced carrying the document identifier and the failure category; confirm a
routine lock conflict produces no such failure record.

**Acceptance Scenarios**:

1. **Given** the file-service rejects a write, **When** a save is attempted,
   **Then** the service emits one error-level structured record tagged
   `event=putfile`, `outcome=write_failed`, including the document identifier.
2. **Given** two editors hold conflicting locks, **When** a save is attempted and
   returns a lock conflict, **Then** no failure record is emitted (this is normal
   collaborative-editing traffic, not an operational failure).
3. **Given** the lock store itself errors during a save, **When** the save is
   attempted, **Then** one error-level record tagged `outcome=lock_repo_error` is
   emitted.

---

### User Story 2 - See when editor tokens fail to issue (Priority: P2)

An on-call engineer needs to distinguish genuine token-issuance failures
(dependency outages, internal errors) from ordinary client rejections (a caller
asking for a document they cannot access or that does not exist), so that alerts
fire on real problems and stay quiet during normal denials.

**Why this priority**: Token issuance is the entry point to editing — if it
fails, no one can open a document — but the failures are noisier to classify, so
the value is in emitting alertable signal for *genuine* failures only.

**Independent Test**: Drive the token endpoint through each failure path and
confirm that genuine failures (internal error, discovery unavailable, token
storage failure) each emit one structured failure record, while expected client
rejections (not found, forbidden, bad request, unsupported file type) emit none.

**Acceptance Scenarios**:

1. **Given** a downstream dependency error during token issuance, **When** a
   token is requested, **Then** one error-level record tagged
   `event=token_issuance` with an `outcome` describing the failure is emitted,
   carrying the document and actor identifiers.
2. **Given** a caller requests a document they are not authorized for, **When** a
   token is requested, **Then** the request is rejected to the caller but **no**
   failure record is emitted.
3. **Given** a caller requests a non-existent document or an unsupported file
   type, **When** a token is requested, **Then** the request is rejected but
   **no** failure record is emitted.

---

### User Story 3 - Know whether Collabora is reachable (Priority: P3)

An on-call engineer needs a clear, current answer to "can the service reach
Collabora right now?" from the health endpoint, and a one-time notification when
that reachability changes — without Collabora being down taking the service
itself out of rotation.

**Why this priority**: This is primarily a diagnostic / root-cause signal that
explains *why* tokens or editor sessions degrade. It is valuable but secondary to
directly observing the token and save failures themselves.

**Independent Test**: Make Collabora unreachable and confirm the health endpoint
reports it as unreachable while still returning a healthy overall status; confirm
exactly one notification is produced on the down transition and one on recovery,
regardless of how long the outage lasts.

**Acceptance Scenarios**:

1. **Given** Collabora is unreachable, **When** the health endpoint is queried,
   **Then** the response reports Collabora as unreachable **and** the overall
   health status remains successful (the service stays in rotation).
2. **Given** Collabora transitions from reachable to unreachable, **When** the
   change occurs, **Then** exactly one warning-level record is emitted; **no**
   further records are emitted while it remains unreachable.
3. **Given** Collabora recovers, **When** reachability is restored, **Then**
   exactly one recovery record is emitted and the health endpoint reports
   Collabora as reachable again with an updated last-success time.
4. **Given** a hard dependency (the service's own database, or its messaging
   connection when configured) is down, **When** the health endpoint is queried,
   **Then** the overall health status is unsuccessful (unchanged from today's
   behavior).

---

### Edge Cases

- **Service starts with Collabora already down**: the health endpoint must report
  Collabora unreachable with no recorded last-success time, and the overall
  status must still be successful.
- **Reachability flapping**: each genuine state change produces exactly one
  record; steady-state (still up, or still down) produces none.
- **Lock conflicts and authorization denials**: never counted or recorded as
  operational failures — they are expected protocol outcomes.
- **No health traffic**: reachability is evaluated only during health-endpoint
  requests; if the endpoint is never called, no probing occurs and reachability is
  neither updated nor logged. In practice the orchestrator polls the endpoint
  continuously, so reachability is bounded by that polling cadence.

## Clarifications

### Session 2026-06-24

- Q: How is Collabora reachability determined and refreshed? → A: Probed once per `/health` request; no background ticker and no self-initiated re-probe, regardless of probe outcome. Reachability is evaluated only when the health endpoint is called.
- Q: What timeout should the per-`/health` Collabora probe use? → A: A short, dedicated probe timeout (~2s), independent of the existing 30s discovery-fetch timeout, so `/health` stays responsive when Collabora hangs.

## Requirements *(mandatory)*

### Functional Requirements

**Save / PutFile failures**

- **FR-001**: The service MUST emit exactly one structured failure record when a
  save fails for an operational reason (file-service write failure, lock-store
  error, or otherwise uncategorized internal error).
- **FR-002**: The service MUST NOT emit a failure record for a save that returns a
  lock conflict or an authorization denial; these are expected outcomes.
- **FR-003**: Each save failure record MUST carry the document identifier and a
  failure category drawn from a fixed, low-cardinality set (`write_failed`,
  `lock_repo_error`, `internal`).

**Token issuance failures**

- **FR-004**: The service MUST emit exactly one structured failure record when
  token issuance fails for a genuine reason (internal error, document-metadata
  lookup failure, discovery/Collabora unavailability, token persistence failure).
- **FR-005**: The service MUST NOT emit a failure record when token issuance is
  rejected for an expected client reason (document not found, authorization
  denied, malformed request, unsupported file type).
- **FR-006**: Each token-issuance failure record MUST carry the document
  identifier, the actor identifier, and a failure category from a fixed,
  low-cardinality set.

**Collabora reachability**

- **FR-007**: The service MUST determine Collabora reachability by performing
  exactly one probe of Collabora during each health-endpoint request, and MUST
  record the resulting reachable/not-reachable state and the time of the last
  successful probe.
- **FR-008**: The service MUST NOT probe Collabora on its own schedule.
  Reachability is evaluated only when the health endpoint is called — there is no
  background ticker and no self-initiated re-probe, regardless of the probe
  outcome.
- **FR-009**: The health endpoint response MUST report Collabora reachability as
  determined by that request's probe, together with the last-success time, and
  MUST NOT change the overall health status based on Collabora reachability
  (Collabora is a soft dependency).
- **FR-010**: The health endpoint MUST continue to report an unsuccessful overall
  status only when a hard dependency (own database; messaging connection when
  configured) is unavailable.
- **FR-011**: The service MUST compare each probe result to the previous one and
  emit exactly one record on a state transition — a warning when reachability is
  lost, an informational record when it is regained — and MUST NOT emit a record
  while the state is unchanged across probes.
- **FR-014**: The Collabora probe MUST use a short, dedicated timeout (~2
  seconds), independent of the existing 30-second discovery-fetch timeout, so that
  a hung or slow Collabora results in a prompt "unreachable" determination without
  delaying the health response or risking the orchestrator's readiness-probe
  timeout.

**Cross-cutting**

- **FR-012**: All three signals MUST share a uniform, queryable field convention
  so a single alert expression can select genuine failures by signal type:
  a stable signal name (`token_issuance`, `putfile`, `collabora_reachability`),
  an outcome/category, correlating identifiers where applicable, and the
  underlying error detail.
- **FR-013**: This feature MUST add no new runtime dependency and MUST NOT change
  any existing HTTP status code or control flow; the only externally visible
  behavioral change is the additional reachability information in the health
  endpoint response.

### Key Entities

- **Health signal record**: a structured log entry representing one genuine
  operational failure or one reachability transition. Attributes: signal name,
  outcome/category, correlating identifiers (document, actor), error detail,
  severity.
- **Collabora reachability state**: the service's current view of Collabora —
  reachable (yes/no) and the timestamp of last successful contact.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of genuine save failures and genuine token-issuance failures
  produce exactly one alertable failure record; routine lock conflicts and
  authorization/not-found/unsupported rejections produce zero failure records.
- **SC-002**: An on-call engineer can determine whether Collabora is reachable
  from a single health-endpoint query, with the answer reflecting a probe
  performed during that same query.
- **SC-003**: A Collabora outage of any duration produces exactly one "lost"
  record and, on recovery, exactly one "regained" record — never a record per
  check.
- **SC-004**: Collabora being unreachable never removes the service from rotation;
  the service continues to issue read-capable tokens and serve existing content
  for as long as its hard dependencies are healthy.
- **SC-005**: A single alert expression keyed on the shared signal convention can
  select genuine failures of each class without matching expected client
  rejections.

## Assumptions

- The audience for these signals is platform operators / on-call engineers using
  a log-aggregation and alerting stack (e.g. Loki/Datadog); no metrics-scraping
  stack is assumed or required for this feature.
- "Hard dependencies" for the health endpoint remain the service's own database
  and its messaging connection (when configured), matching current behavior;
  Collabora and the file-service are treated as soft dependencies for the purpose
  of the overall health status.
- The existing per-request access logging remains the place where ordinary,
  non-failure outcomes (including client rejections and lock conflicts) stay
  visible; this feature deliberately does not duplicate them as failure records.
- A future move to a metrics-scraping stack is anticipated; the chosen failure and
  reachability observation points are intended to be the same points where
  counters/gauges would later be emitted, so no rework of those points is expected.
- Collabora reachability is probed synchronously during each health-endpoint
  request; there is no background scheduler and no reachability-refresh
  configuration. (The orchestrator's existing health-poll cadence is therefore
  what bounds how current the reachability signal is.)
