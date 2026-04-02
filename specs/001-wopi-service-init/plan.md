# Implementation Plan: Initial WOPI Service

**Branch**: `001-wopi-service-init` | **Date**: 2026-03-30 (revised) | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/001-wopi-service-init/spec.md`

## Summary

Implement a Go WOPI host service using hexagonal architecture that
enables Collabora Online document editing within the Alkemio platform.
The service exposes a token issuance endpoint (behind Oathkeeper) and
WOPI REST endpoints (CheckFileInfo, GetFile, PutFile, Lock/Unlock).
Authorization is checked via NATS auth-evaluation-service. Document
metadata comes from Alkemio's PostgreSQL (read-only). File content is
read/written through file-service-go. Access tokens and locks are
managed in the service's own PostgreSQL database (sqlc/pgx v5).

## Technical Context

**Language/Version**: Go 1.26
**Primary Dependencies**: chi v5 (HTTP router), pgx v5 (PostgreSQL driver), sqlc (query generation), golang-migrate (migrations), zap (structured logging), nats.go (NATS client, optional), golang.org/x/net/http2 (h2c), sony/gobreaker (circuit breaker)
**Storage**: Own PostgreSQL for local state (access tokens, locks, sessions); document metadata and file content via file-service-go
**Testing**: `go test` with table-driven tests; in-memory adapters, pgxmock, in-process NATS for unit tests
**Target Platform**: Linux server (containerized)
**Project Type**: Web service (WOPI host)
**Performance Goals**: Low-frequency WOPI requests (handful per minute per document)
**Constraints**: Must integrate with Oathkeeper, authorization-evaluation-service (h2c or NATS), and file-service-go
**Scale/Scope**: Single-service deployment alongside Alkemio stack

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Hexagonal Architecture | PASS | Domain layer with ports/adapters; adapters for HTTP, Postgres, NATS, file-service-go |
| II. WOPI Protocol Compliance | PASS | All required endpoints specified in contracts |
| III. Alkemio Integration First | PASS | Auth via NATS, files via file-service-go, metadata via Alkemio DB |
| IV. Type-Safe Database Access | PASS | sqlc + pgx v5; .sql files for queries; golang-migrate for migrations |
| V. Security by Design | PASS | Token validation on every WOPI request; proof key validation; Oathkeeper JWT on token issuance; NATS auth check |
| VI. Test-First Development | PASS | Tests before implementation per workflow |
| VII–XV | PASS | Process/quality principles — all applicable |

## Project Structure

### Documentation (this feature)

```text
specs/001-wopi-service-init/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── wopi-endpoints.md
│   └── integration-services.md
└── tasks.md
```

### Source Code (repository root)

```text
cmd/
└── server/
    └── main.go                             # Entry point, DI wiring, migration runner

internal/
├── domain/
│   ├── model/
│   │   ├── token.go                        # AccessToken entity
│   │   ├── lock.go                         # Lock entity
│   │   ├── session.go                      # WOPISession entity
│   │   ├── fileinfo.go                     # FileInfo value object (CheckFileInfo response)
│   │   └── document.go                     # Document value object (from Alkemio DB)
│   ├── port/
│   │   ├── token_repository.go             # Driven port: token CRUD
│   │   ├── lock_repository.go              # Driven port: lock CRUD
│   │   ├── session_repository.go           # Driven port: session CRUD
│   │   ├── document_repository.go          # Driven port: Alkemio DB document lookup
│   │   ├── auth_service.go                 # Driven port: NATS authorization check
│   │   ├── file_service.go                 # Driven port: file-service-go read/write
│   │   └── discovery_client.go             # Driven port: Collabora discovery
│   └── service/
│       ├── wopi_service.go                 # Core WOPI use cases
│       ├── token_service.go                # Token generation, validation, issuance
│       └── discovery_service.go            # Discovery caching logic
├── adapter/
│   ├── inbound/
│   │   └── http/
│   │       ├── router.go                   # chi router setup
│   │       ├── token_handler.go            # POST /wopi/token (behind Oathkeeper)
│   │       ├── wopi_handler.go             # WOPI endpoint handlers
│   │       ├── discovery_handler.go        # Discovery endpoint handler
│   │       ├── health_handler.go           # Health check handler
│   │       ├── middleware_auth.go          # WOPI access token validation
│   │       ├── middleware_proof.go         # WOPI proof key validation
│   │       └── middleware_jwt.go           # Oathkeeper JWT extraction
│   └── outbound/
│       ├── postgres/
│       │   ├── queries/
│       │   │   ├── tokens.sql
│       │   │   ├── locks.sql              # CAS operations with lock_id in WHERE
│       │   │   └── sessions.sql
│       │   ├── generated/                  # sqlc output (committed)
│       │   ├── token_repository.go
│       │   ├── lock_repository.go
│       │   └── session_repository.go
│       ├── authhttp/
│       │   └── auth_service.go             # h2c HTTP auth client (preferred)
│       ├── nats/
│       │   └── auth_service.go             # NATS auth client (fallback)
│       ├── authbreaker/
│       │   └── breaker.go                  # Circuit breaker wrapper (shared)
│       ├── fileservice/
│       │   └── file_client.go              # HTTP client for file-service-go
│       └── collabora/
│           └── discovery_client.go         # HTTP client for Collabora discovery XML
└── config/
    └── config.go                           # Environment config struct

migrations/
├── 000001_create_access_tokens.up.sql
├── 000001_create_access_tokens.down.sql
├── 000002_create_locks.up.sql
├── 000002_create_locks.down.sql
├── 000003_create_wopi_sessions.up.sql
└── 000003_create_wopi_sessions.down.sql

.github/
└── workflows/
    ├── build-push-ghcr-pr.yml
    ├── build-deploy-k8s-dev-hetzner.yml
    ├── build-deploy-k8s-test-hetzner.yml
    ├── build-deploy-k8s-sandbox-hetzner.yml
    └── build-release-docker-hub.yml

Dockerfile
docker-compose.yml
.golangci.yml                               # Already exists
sqlc.yaml
go.mod
go.sum
```

**Structure Decision**: Hexagonal architecture with `internal/` for
compiler-enforced encapsulation. Domain layer has zero infrastructure
imports. Adapters split into inbound (HTTP) and outbound (own Postgres,
Alkemio DB, NATS, file-service-go HTTP, Collabora HTTP). Single
`cmd/server/` entry point handles DI wiring and migration execution.

## Complexity Tracking

No constitution violations. No complexity justifications needed.
