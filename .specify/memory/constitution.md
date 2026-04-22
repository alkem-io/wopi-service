<!--
Sync Impact Report
- Version change: N/A → 1.0.0 → 1.1.0 → 1.2.0 (principles added)
- Added principles:
  - I. Hexagonal Architecture
  - II. WOPI Protocol Compliance
  - III. Alkemio Integration First
  - IV. Type-Safe Database Access
  - V. Security by Design
  - VI. Test-First Development
  - VII. Root Cause Analysis (NON-NEGOTIABLE)
  - VIII. DRY — Single Source of Truth (expanded from DRY)
  - IX. Lint on Completion
  - X. No Legacy Code
  - XI. No Busywork
  - XII. Meaningful Tests Only
  - XIII. Meaningful Success Criteria
  - XIV. Latest Dependencies Always
  - XV. No Assumptions
- Added sections:
  - Technology Stack Constraints
  - Integration Requirements
- Templates requiring updates:
  - .specify/templates/plan-template.md — ✅ no updates needed (generic)
  - .specify/templates/spec-template.md — ✅ no updates needed (generic)
  - .specify/templates/tasks-template.md — ✅ no updates needed (generic)
  - No command files found in .specify/templates/commands/
- Integration Requirements enriched with concrete Alkemio server
  patterns (Ory Kratos, RabbitMQ, storage adapter, policy engine)
- Follow-up TODOs: none
-->

# Alkemio WOPI Service Constitution

## Core Principles

### I. Hexagonal Architecture

All code MUST follow the hexagonal (ports and adapters) architecture
pattern. Business logic lives in the domain core and MUST NOT depend
on external infrastructure. External systems (database, HTTP, Alkemio
APIs, Collabora) are accessed exclusively through well-defined ports
(interfaces) with concrete adapters.

- Domain types and interfaces MUST reside in dedicated domain packages
  with zero infrastructure imports.
- Each external dependency MUST have its own adapter implementing a
  domain-defined port.
- No adapter MAY import another adapter directly; cross-cutting
  concerns flow through the domain or application layer.

### II. WOPI Protocol Compliance

The service MUST implement the WOPI (Web Application Open Platform
Interface) protocol correctly and completely for all supported
operations. Protocol behavior MUST match the WOPI specification as
published by Microsoft.

- All WOPI endpoints (CheckFileInfo, GetFile, PutFile, Lock,
  Unlock, RefreshLock, etc.) MUST conform to the specification's
  request/response contracts.
- WOPI discovery and proof validation MUST be implemented per spec.
- Protocol compliance MUST be verified via integration tests against
  a real Collabora instance where feasible.

### III. Alkemio Integration First

This service exists to serve the Alkemio platform. Authorization and
storage operations MUST delegate to Alkemio's existing systems rather
than reimplementing them.

- File access authorization MUST be validated against Alkemio's
  authorization system for every WOPI request.
- File storage MUST use Alkemio's storage management layer; the WOPI
  service MUST NOT maintain its own independent file store.
- User identity MUST be resolved through Alkemio's authentication
  tokens/mechanisms.

### IV. Type-Safe Database Access

All database interactions MUST use sqlc for query generation and pgx
as the PostgreSQL driver. Hand-written SQL queries outside of sqlc are
prohibited except for migration files.

- SQL queries MUST be defined in `.sql` files and compiled via sqlc.
- Database schema changes MUST use versioned migrations.
- The pgx connection pool MUST be configured at the application layer
  and injected into adapters via the hexagonal architecture.

### V. Security by Design

The WOPI service handles document access and editing, making security
a non-negotiable concern at every layer.

- All WOPI access tokens MUST be validated on every request; no
  endpoint MAY skip token verification.
- WOPI proof keys MUST be validated to confirm requests originate
  from a trusted WOPI client (Collabora).
- Secrets, tokens, and credentials MUST NOT be logged or included in
  error responses.
- All inter-service communication MUST use TLS in production.

### VI. Test-First Development

Tests MUST be written before implementation for all new features.
The red-green-refactor cycle is the standard workflow.

- Unit tests MUST cover domain logic with no infrastructure
  dependencies (use in-memory adapters or mocks for ports).
- Integration tests MUST verify adapter behavior against real
  dependencies (database, Collabora) where feasible.
- WOPI protocol compliance tests MUST validate endpoint behavior
  against the specification.

### VII. Root Cause Analysis (NON-NEGOTIABLE)

All debugging and bug fixing MUST be driven by root cause analysis.
Opportunistic or speculative code changes hoping they might resolve
an issue are strictly forbidden.

- Before any fix is applied, the actual root cause MUST be
  identified and documented with evidence.
- If the root cause is unclear, invest time in debugging first —
  guessing wastes more time than investigating.
- Fixes MUST directly address the identified root cause, not
  symptoms.
- Every bug fix commit MUST be traceable to a specific diagnosed
  cause.

### VIII. DRY — Single Source of Truth

Code duplication is treated as a defect. When two or more methods
share substantially the same logic, that logic MUST be extracted
into a shared helper or refactored to eliminate the duplication.

- No two methods MAY implement the same logic in different modules.
- When methods share partial logic, the common part MUST be
  extracted to a shared helper.
- Before implementing new logic, search for existing
  implementations — extend rather than duplicate.
- Configuration, constants, and type definitions MUST live in one
  canonical location.
- Duplicated code paths MUST be identified during review and
  refactored before merge.
- Three similar lines of inline code are acceptable; duplicated
  multi-line blocks are not.

### IX. Lint on Completion

Every piece of code MUST pass linting before it is considered
ready. Linting is not a CI-only gate — it MUST be run locally
when a unit of work (function, file, feature slice) is complete.

- Code MUST pass `golangci-lint run` (or the project-configured
  linter) with zero violations before committing.
- Linter configuration is part of the project and MUST NOT be
  bypassed with `nolint` directives unless justified in a comment.

### X. No Legacy Code

We control the full stack and all consumers. Never silently assume
backward compatibility is required.

- Dead, deprecated, or unused code MUST be removed — not left
  "just in case."
- Backward-compatibility hacks, unused exports, commented-out code,
  and defensive code for scenarios that no longer apply MUST be
  deleted.
- When a feature requires changes across multiple services,
  coordinate those changes rather than maintaining compatibility
  shims.
- Every line of code MUST justify its existence.

### XI. No Busywork

Every task, test, and artifact MUST deliver demonstrable value.

- Reject make-work activities that exist only to satisfy process
  checkboxes.
- Do not create documentation, comments, or abstractions "just in
  case."
- Specifications MUST be lean: only what is necessary to
  communicate intent.

### XII. Meaningful Tests Only

Tests MUST defend real invariants or catch real regressions.

- Never write tests for the sake of coverage metrics.
- Do not test implementation details, trivial getters/setters, or
  scenarios that cannot fail.
- If a test does not help catch bugs or document critical behavior,
  do not write it.

### XIII. Meaningful Success Criteria

Success criteria MUST be directly testable within this service.

- Never invent arbitrary metrics without baseline measurements or
  explicit stakeholder requirements.
- Avoid vanity metrics or external business outcomes that cannot be
  validated during development.

### XIV. Latest Dependencies Always

When adding or updating any dependency, the latest stable version
MUST be verified online (pkg.go.dev, GitHub releases, etc.).

- Never rely on AI training data for version numbers — it is likely
  outdated.
- Production/runtime dependencies MUST be pinned to specific
  versions, and those versions MUST be current at time of addition.
- Exception: CI tooling (linters, code generators) MAY
  intentionally track `latest` when documented, to detect upstream
  breaking changes early.

### XV. No Assumptions

Never assume requirements, behavior, or implementation details that
are not explicitly defined.

- If something is unclear or unknown, ask the user for
  clarification before proceeding.
- If factual information is needed (versions, API specs, library
  behavior), search online to verify.
- Do not guess — guessing leads to rework; asking or searching
  takes less time than fixing wrong assumptions.

## Anti-Patterns — Quick Reference

The following are **strictly prohibited** (derived from principles
VII–XV):

1. Do not apply speculative fixes — find root cause first
2. Do not keep code "just in case" or for backward compatibility
   unless explicitly requested
3. Do not duplicate logic — find or create a single shared
   implementation
4. Do not add superficial tests for coverage padding
5. Do not invent performance SLAs without evidence
6. Do not create abstractions for hypothetical future needs
7. Do not add comments explaining obvious code
8. Do not rely on training data for dependency versions — check
   online
9. Do not create documentation files unless explicitly requested
10. Do not assume — ask or search when something is unclear
11. Do not use `map[string]any` for HTTP response bodies — use named
    structs with JSON tags. This enables OpenAPI spec generation and
    provides compile-time type safety. Each response type MUST have
    a `Render(w http.ResponseWriter)` method.

## Technology Stack Constraints

The following technology choices are fixed and MUST NOT be replaced
without a constitution amendment:

| Component        | Technology               |
|------------------|--------------------------|
| Language         | Go 1.26                  |
| Database driver  | pgx v5                   |
| Query generation | sqlc                     |
| Database         | PostgreSQL               |
| Architecture     | Hexagonal (ports/adapters)|
| HTTP router      | chi v5                   |
| Logging          | Zap (structured)         |
| Authorization    | NATS via auth-evaluation-service |
| File I/O         | file-service-go (HTTP, cluster-internal) |
| WOPI client      | Collabora Online (primary)|
| Identity         | Oathkeeper JWT (alkemio_actor_id) |

Additional dependencies SHOULD be minimized. The Go standard library
MUST be preferred over third-party packages when functionality is
equivalent.

## Integration Requirements

The WOPI service integrates with two primary external systems:

**Oathkeeper** (reverse proxy):
- Sits in front of the token issuance endpoint.
- Injects JWT with `alkemio_actor_id` claim into Authorization
  header after authenticating via Kratos.
- WOPI protocol endpoints (called by Collabora) are NOT routed
  through Oathkeeper — they use opaque access tokens.

**Authorization Evaluation Service** (Go, NATS):
- Subject: `auth.evaluate`
- Input: `{actorId, privilege, authorizationPolicyId}`
- Output: `{allowed, reason}`
- Used to check READ and UPDATE_CONTENT privileges before issuing
  WOPI access tokens.

**Alkemio PostgreSQL Database** (read-only):
- Document table provides externalID, authorizationPolicyId,
  displayName, mimeType, and size for WOPI operations.

**file-service-go** (Go, HTTP, cluster-internal):
- Private endpoints for file metadata and content.
- Metadata via `GET /internal/file/:id/meta`.
- GetFile reads content via `GET /internal/file/:id/content`.
- PutFile writes content via `PUT /internal/file/:id/content`
  (store-and-link: replaces file + updates document record).

**Collabora Online**:
- Acts as the WOPI client consuming this service's WOPI endpoints.
- Discovery endpoint provides editor capabilities and URL templates.
- Proof key validation ensures request authenticity.

**Communication patterns**:
- NATS for authorization checks (auth-evaluation-service).
- HTTP for file I/O (file-service-go private endpoints).
- HTTP for WOPI protocol endpoints (Collabora → this service).
- Communication contracts MUST be documented and versioned.

## Governance

This constitution is the authoritative guide for all development
decisions in the Alkemio WOPI Service. It supersedes informal
conventions and ad-hoc decisions.

- **Amendments**: Any change to this constitution MUST be documented
  with a version bump, rationale, and migration plan for affected
  code.
- **Versioning**: The constitution follows semantic versioning.
  MAJOR for principle removals/redefinitions, MINOR for additions
  or material expansions, PATCH for clarifications.
- **Compliance**: All pull requests MUST be reviewed for compliance
  with these principles. Violations MUST be justified in the PR
  description and tracked as tech debt if accepted.
- **Review cadence**: The constitution SHOULD be reviewed quarterly
  or when significant architectural decisions arise.

**Version**: 1.2.0 | **Ratified**: 2026-03-30 | **Last Amended**: 2026-03-30
