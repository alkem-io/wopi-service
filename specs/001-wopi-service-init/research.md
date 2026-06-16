# Research: Initial WOPI Service Implementation

**Date**: 2026-03-30 (revised)
**Feature**: 001-wopi-service-init

## 1. HTTP Router

**Decision**: chi v5 (`github.com/go-chi/chi/v5`)
**Rationale**: Lightweight, stdlib-compatible (`http.Handler`), clean URL
parameter extraction, excellent middleware chaining. WOPI endpoints
follow REST patterns that chi handles naturally.
**Alternatives considered**:
- `net/http` (Go 1.22+): Viable but lacks middleware composition.
- gorilla/mux: Community trust eroded after 2022 archival.
- echo: Full framework, overkill for WOPI.

## 2. Migration Tool

**Decision**: golang-migrate with `embed.FS`
**Rationale**: Mature, supports embedding migrations in binary, works
with sqlc (reads migration dir for schema inference). Runs at startup.

## 3. Project Structure (Hexagonal)

**Decision**: `internal/` with domain/adapter split

```
cmd/server/main.go
internal/
  domain/
    model/                                  # Entities, value objects
    port/                                   # Interfaces (driven + driving)
    service/                                # Use case implementations
  adapter/
    inbound/
      http/                                 # WOPI handlers, router, middleware
    outbound/
      postgres/
        queries/                            # .sql files for sqlc
        generated/                          # sqlc output (committed)
        repository.go                       # Implements domain ports
      alkemiodb/                            # Read-only Alkemio DB adapter
      nats/                                 # Auth-evaluation-service adapter
      fileservice/                          # file-service HTTP client adapter
      collabora/                            # Discovery HTTP client adapter
  config/                                   # Environment config loading
migrations/                                 # SQL migration files
sqlc.yaml
```

## 4. Authentication — Oathkeeper

**Decision**: Extract `alkemio_actor_id` from Oathkeeper-injected JWT
**Rationale**: Oathkeeper already sits in front of all Alkemio services
and handles authentication via Kratos. The JWT contains
`alkemio_actor_id` in the claims. No need for a separate WHO RabbitMQ
call.
**JWT claims used**: `alkemio_actor_id` (actor UUID)
**JWKS validation**: Against Oathkeeper's JWKS endpoint
**Applies to**: Token issuance endpoint only. WOPI protocol endpoints
use opaque access tokens (not JWTs).

## 5. Authorization — NATS Auth-Evaluation-Service

**Decision**: NATS request-reply on subject `auth.evaluate`
**Rationale**: The authorization-evaluation-service is already deployed,
uses NATS, and provides the exact interface needed: check if an agent
has a specific privilege on a resource's authorization policy. This
eliminates the need for RabbitMQ INFO pattern.
**Reference**: `/Users/antst/work/alkemio/authorization-evaluation-service`
**Key details**:
- Request: `{actorId, privilege, authorizationPolicyId}`
- Response: `{allowed: bool, reason: string}`
- Privileges used: `read`, `update-content`
- Has circuit breaker, retry logic, LRU policy cache built in

## 6. File I/O — file-service

**Decision**: HTTP calls to file-service private endpoints
**Rationale**: file-service is the universal file I/O gateway for
the Alkemio platform. Using it decouples the WOPI service from the
actual storage backend (local, S3, etc.).
**Reference**: `/Users/antst/work/alkemio/file-service`
**Endpoints used**:
- `GET /internal/storage/:externalID` — read file content (for GetFile)
- `PUT /internal/storage/document/:documentId` — write file + update
  document record atomically (for PutFile)
- `HEAD /internal/storage/:externalID` — check file exists

## 7. Document Metadata — Alkemio DB (read-only)

**Decision**: Direct read-only PostgreSQL connection to Alkemio's DB
**Rationale**: Same pattern as authorization-evaluation-service. The
WOPI service needs document metadata (externalID, authorizationPolicyId,
displayName, mimeType) for every CheckFileInfo call and for token
issuance. A read-only DB connection is simpler and faster than any
message-based alternative.
**Tables accessed**: `document` (read-only)

## 8. Zap Logger Integration

**Decision**: Explicit dependency injection, no globals
**Rationale**: Create `*zap.Logger` in `cmd/server/main.go`, pass to
all constructors. Use child loggers for component scoping.

## 9. WOPI Protocol Details

### Endpoint Routing
All lock operations use `POST /wopi/files/{file_id}` — dispatch on
`X-WOPI-Override` header. See `contracts/wopi-endpoints.md`.

### CheckFileInfo Required Fields
`BaseFileName`, `OwnerId`, `Size`, `UserId`, `Version`

### Access Token Format
URL-safe Base64 (alphanumeric + `-` + `_`). Passed via `access_token`
query parameter. Optional `access_token_ttl` (UNIX ms).

### Proof Key Validation
RSA SHA-256. Public keys from Collabora discovery XML. Three
verification combos. 20-minute timestamp window. See earlier
research for full details.

### Collabora-Specific Extensions
- `X-COOL-WOPI-Timestamp` on PutFile response
- Conflict: 409 with `{"COOLStatusCode": 1010}`
- `X-COOL-WOPI-IsAutosave` / `X-COOL-WOPI-IsModifiedByUser`
- Discovery at `/hosting/discovery`, cache 12-24 hours

## 10. Configuration

**Decision**: Environment variables (12-factor app)

Own database (matching oidc-service pattern, `WOPI_` prefix):
- `WOPI_DATABASE_HOST/PORT/USERNAME/PASSWORD/NAME/TIMEOUT`

Alkemio DB (read-only):
- `ALKEMIO_DATABASE_HOST/PORT/USERNAME/PASSWORD/NAME`

NATS:
- `NATS_URL` — NATS server URL

File service:
- `FILE_SERVICE_URL` — file-service base URL

Service-specific:
- `WOPI_COLLABORA_URL` — Collabora Online base URL
- `WOPI_BASE_URL` — This service's externally-reachable WOPI URL
- `WOPI_TOKEN_SECRET` — Secret for generating opaque access tokens
- `WOPI_SERVER_PORT` — HTTP listen port

## 11. CI/CD Workflows

**Reference**: `/Users/antst/work/alkemio/matrix-adapter-go/.github/workflows/`

Workflows to replicate (excluding TS library publishing):

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `build-push-ghcr-pr.yml` | PR | Build + push to GHCR |
| `build-deploy-k8s-dev-hetzner.yml` | Push to develop | Deploy to K8s dev |
| `build-deploy-k8s-test-hetzner.yml` | Manual | Deploy to test |
| `build-deploy-k8s-sandbox-hetzner.yml` | Manual | Deploy to sandbox |
| `build-release-docker-hub.yml` | Release tag | DockerHub publish |

Dockerfile: multi-stage Go 1.25-alpine → distroless
.golangci.yml: already exists in repo
