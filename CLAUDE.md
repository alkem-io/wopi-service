# Alkemio WOPI Service

Go microservice implementing the WOPI protocol for Collabora Online
integration into the Alkemio platform.

## Tech Stack

- **Language**: Go 1.26
- **Database**: PostgreSQL, pgx v5 driver, sqlc for query generation
- **Authorization**: h2c HTTP (default) or NATS (legacy) to authorization-evaluation-service
- **Identity**: Oathkeeper JWT (`alkemio_actor_id` claim) on token issuance endpoint
- **File I/O**: file-service-go private endpoints (HTTP, cluster-internal)
- **Logging**: Zap (structured)
- **HTTP Router**: chi v5
- **Architecture**: Hexagonal (ports and adapters)
- **Alkemio DB**: Read-only access for document metadata lookup

## Architecture Rules

- Domain core has zero infrastructure imports
- External systems accessed exclusively through ports (interfaces)
  with concrete adapters
- No adapter imports another adapter directly
- SQL queries defined in `.sql` files compiled via sqlc — no
  hand-written queries outside migrations

## Endpoint Architecture

**Behind Oathkeeper** (actor identity from JWT):
- `POST /wopi/token` — issue WOPI access token for a document

**WOPI protocol** (opaque access token, called by Collabora):
- `GET /wopi/files/{file_id}` — CheckFileInfo
- `GET /wopi/files/{file_id}/contents` — GetFile
- `POST /wopi/files/{file_id}/contents` — PutFile
- `POST /wopi/files/{file_id}` — Lock/Unlock/RefreshLock/UnlockAndRelock
- `GET /wopi/discovery` — Discovery proxy
- `GET /health` — Health check

## Anti-Patterns — Strictly Prohibited

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

## Development Workflow

- Always run `golangci-lint run` before committing
- Tests must defend real invariants — no coverage-padding tests
- Root cause analysis is mandatory before any bug fix; document the
  cause with evidence
- Verify latest dependency versions online (pkg.go.dev, GitHub
  releases) — never trust training data
- If something is unclear, ask or search — do not guess
- Use `actorId` internally, never `userId`

## Integration Context

- Auth via NATS `auth.evaluate` (agentId + privilege +
  authorizationPolicyId)
- Document metadata from Alkemio PostgreSQL (read-only user)
- File content via file-service-go private endpoints
  (`GET /internal/storage/:externalID`,
  `PUT /internal/storage/document/:documentId`)
- Actor identity from Oathkeeper JWT (`alkemio_actor_id` claim)
- WOPI proof key validation required on all requests from Collabora
- Oathkeeper config at
  `/Users/antst/work/alkemio/server/.build/ory/oathkeeper/`

## Configuration (env vars)

Database (own, matching oidc-service pattern):
- `WOPI_DATABASE_HOST/PORT/USERNAME/PASSWORD/NAME/TIMEOUT`

Alkemio DB (read-only):
- `ALKEMIO_DATABASE_HOST/PORT/USERNAME/PASSWORD/NAME`

Authorization (mutually exclusive — h2c is default):
- `AUTH_SERVICE_URL` (default: `http://authorization-evaluation-service:6060`)
- `NATS_URL` (if set, uses NATS transport instead of h2c)

File service:
- `FILE_SERVICE_URL` (e.g., `http://file-service-go:4003`)

Service:
- `WOPI_COLLABORA_URL`, `WOPI_BASE_URL`, `WOPI_TOKEN_SECRET`,
  `WOPI_SERVER_PORT`

## Full Constitution

See `.specify/memory/constitution.md` for the complete set of
principles and governance rules.
