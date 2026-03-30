# Tasks: Initial WOPI Service Implementation

**Input**: Design documents from `/specs/001-wopi-service-init/`
**Prerequisites**: plan.md, spec.md, data-model.md, contracts/

**Tests**: Included — constitution mandates test-first development (Principle VI).

**Organization**: Tasks grouped by user story for independent implementation and testing.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story (US1, US2, US3)

## Path Conventions

- Single Go service: `cmd/server/`, `internal/`, `migrations/` at repository root

---

## Phase 1: Setup

**Purpose**: Project initialization, Go module, config, build tooling

- [x] T001 Initialize Go module (`go mod init github.com/alkem-io/wopi-service`) and add dependencies (chi v5, pgx v5, sqlc, golang-migrate, zap, nats.go) — verify latest versions online via pkg.go.dev before pinning in `go.mod`
- [x] T002 Create `sqlc.yaml` at project root configured for pgx/v5, queries from `internal/adapter/outbound/postgres/queries/`, output to `internal/adapter/outbound/postgres/generated/`, schema from `migrations/`
- [x] T003 [P] Create environment config struct and loader in `internal/config/config.go` — own DB vars (`WOPI_DATABASE_*`), Alkemio DB vars (`ALKEMIO_DATABASE_*`), NATS (`NATS_URL`), file-service (`FILE_SERVICE_URL`), service vars (`WOPI_COLLABORA_URL`, `WOPI_BASE_URL`, `WOPI_TOKEN_SECRET`, `WOPI_SERVER_PORT`)
- [x] T004 [P] Create `Dockerfile` (multi-stage: Go 1.25-alpine builder → distroless runtime, multi-arch amd64+arm64) based on matrix-adapter-go pattern
- [x] T005 [P] Create `docker-compose.yml` for local dev with PostgreSQL, NATS, and this service

**Checkpoint**: Project builds, config loads from environment, containerizable

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Database schema, domain model, ports, outbound adapters — MUST complete before any user story

**No user story work can begin until this phase is complete**

### Database & Migrations

- [x] T006 Create migration `migrations/000001_create_access_tokens.up.sql` and `.down.sql` per data-model.md (UUID PK, token UNIQUE, file_id INDEX, expires_at INDEX)
- [x] T007 [P] Create migration `migrations/000002_create_locks.up.sql` and `.down.sql` per data-model.md (UUID PK, file_id UNIQUE, lock_id, expires_at INDEX)
- [x] T008 [P] Create migration `migrations/000003_create_wopi_sessions.up.sql` and `.down.sql` per data-model.md (UUID PK, file_id INDEX, token_id FK)
- [x] T009 Implement migration runner in `cmd/server/main.go` using golang-migrate with `embed.FS` — migrations run at startup

### Domain Model

- [x] T010 [P] Create AccessToken entity in `internal/domain/model/token.go`
- [x] T011 [P] Create Lock entity in `internal/domain/model/lock.go`
- [x] T012 [P] Create WOPISession entity in `internal/domain/model/session.go`
- [x] T013 [P] Create FileInfo value object in `internal/domain/model/fileinfo.go` (CheckFileInfo response: BaseFileName, OwnerId, Size, UserId, Version, UserCanWrite, SupportsLocks, SupportsUpdate, LastModifiedTime, etc.)
- [x] T014 [P] Create Document value object in `internal/domain/model/document.go` (from Alkemio DB: id, externalID, displayName, mimeType, size, authorizationPolicyId)

### Domain Ports (Interfaces)

- [x] T015 [P] Define TokenRepository port in `internal/domain/port/token_repository.go` (Create, FindByToken, DeleteByID, DeleteExpired)
- [x] T016 [P] Define LockRepository port in `internal/domain/port/lock_repository.go` (Create, FindByFileID, Update, DeleteByFileID, DeleteExpired)
- [x] T017 [P] Define SessionRepository port in `internal/domain/port/session_repository.go` (Create, FindByFileID, DeleteByTokenID)
- [x] T018 [P] Define DocumentRepository port in `internal/domain/port/document_repository.go` (FindByID — Alkemio DB read-only)
- [x] T019 [P] Define AuthService port in `internal/domain/port/auth_service.go` (CheckPrivilege: agentId, privilege, authorizationPolicyId → allowed)
- [x] T020 [P] Define FileService port in `internal/domain/port/file_service.go` (ReadFile: externalID → content, WriteFile: documentId + content → externalID, FileExists: externalID → bool)
- [x] T021 [P] Define DiscoveryClient port in `internal/domain/port/discovery_client.go` (FetchDiscovery)

### Outbound Adapters — Own Database

- [x] T022 Write sqlc query file `internal/adapter/outbound/postgres/queries/tokens.sql` (insert, find_by_token, delete_by_id, delete_expired)
- [x] T023 [P] Write sqlc query file `internal/adapter/outbound/postgres/queries/locks.sql` (upsert, find_by_file_id, update_expiry, delete_by_file_id, delete_expired)
- [x] T024 [P] Write sqlc query file `internal/adapter/outbound/postgres/queries/sessions.sql` (insert, find_by_file_id, delete_by_token_id)
- [x] T025 Run `sqlc generate` and commit generated code in `internal/adapter/outbound/postgres/generated/`
- [x] T026 Implement TokenRepository adapter in `internal/adapter/outbound/postgres/token_repository.go`
- [x] T027 [P] Implement LockRepository adapter in `internal/adapter/outbound/postgres/lock_repository.go`
- [x] T028 [P] Implement SessionRepository adapter in `internal/adapter/outbound/postgres/session_repository.go`

### Outbound Adapters — External Services

- [x] T029 Implement DocumentRepository adapter in `internal/adapter/outbound/alkemiodb/document_repository.go` — read-only pgx connection to Alkemio DB, query document table by ID
- [x] T030 [P] Implement AuthService adapter in `internal/adapter/outbound/nats/auth_service.go` — NATS request-reply on `auth.evaluate` per contracts/integration-services.md
- [x] T031 [P] Implement FileService adapter in `internal/adapter/outbound/fileservice/file_client.go` — HTTP client for file-service-go private endpoints (GET/PUT/HEAD)
- [x] T032 [P] Implement DiscoveryClient adapter in `internal/adapter/outbound/collabora/discovery_client.go` — HTTP GET to Collabora `/hosting/discovery`, parse XML

### Application Entry Point

- [x] T033 Create `cmd/server/main.go` — initialize zap logger, load config, create pgxpool (own DB + Alkemio DB), connect NATS, run migrations, wire all adapters and domain services, start HTTP server with graceful shutdown

**Checkpoint**: Foundation ready — all ports defined, adapters implemented, service starts and connects

---

## Phase 3: User Story 1 — Open Document for Editing in Collabora (Priority: P1) MVP

**Goal**: Token issuance + full round-trip: CheckFileInfo → GetFile → PutFile

**Independent Test**: Request token, then issue WOPI requests with valid/invalid tokens, verify correct responses

### Tests for User Story 1

- [x] T034 [P] [US1] Write unit tests for TokenService in `internal/domain/service/token_service_test.go` — token generation (URL-safe format), validation (valid/expired/missing), permission checking, issuance flow (JWT extraction → auth check → token creation)
- [x] T035 [P] [US1] Write unit tests for WOPIService CheckFileInfo/GetFile/PutFile in `internal/domain/service/wopi_service_test.go` — use in-memory adapter mocks for ports
- [x] T036 [P] [US1] Write integration test for token issuance handler in `internal/adapter/inbound/http/token_handler_test.go` — valid JWT → 200 with token, missing JWT → 401, no permission → 403, missing doc → 404
- [x] T037 [P] [US1] Write integration test for WOPI handler endpoints in `internal/adapter/inbound/http/wopi_handler_test.go` — CheckFileInfo, GetFile, PutFile with auth middleware (valid token → 200, invalid → 401, expired → 401)

### Implementation for User Story 1

- [x] T038 [US1] Implement TokenService in `internal/domain/service/token_service.go` — GenerateToken (URL-safe Base64, 8h TTL), ValidateToken (DB lookup, expiry check), IssueToken (extract actor from JWT, lookup document, check auth via NATS, create token)
- [x] T039 [US1] Implement WOPIService core use cases in `internal/domain/service/wopi_service.go` — CheckFileInfo (lookup document in Alkemio DB, build FileInfo response), GetFile (get externalID from Alkemio DB, read from file-service-go), PutFile (validate lock, write via file-service-go store-and-link)
- [x] T040 [US1] Implement Oathkeeper JWT extraction middleware in `internal/adapter/inbound/http/middleware_jwt.go` — validate JWT via JWKS, extract `alkemio_actor_id`, inject into request context
- [x] T041 [US1] Implement token issuance handler in `internal/adapter/inbound/http/token_handler.go` — POST /wopi/token, extract actor from JWT middleware, call TokenService.IssueToken, return token + TTL + wopiSrc
- [x] T042 [US1] Implement WOPI access token validation middleware in `internal/adapter/inbound/http/middleware_auth.go` — extract `access_token` from query param, validate via TokenService, inject actor/file context
- [x] T043 [US1] Implement WOPI proof key validation middleware in `internal/adapter/inbound/http/middleware_proof.go` — verify X-WOPI-Proof/ProofOld/TimeStamp headers (RSA SHA-256, 20min window)
- [x] T044 [US1] Implement WOPI endpoint handlers in `internal/adapter/inbound/http/wopi_handler.go` — CheckFileInfo (GET), GetFile (GET /contents), PutFile (POST /contents), dispatch on X-WOPI-Override
- [x] T045 [US1] Implement health check handler in `internal/adapter/inbound/http/health_handler.go` — check own DB, NATS, file-service-go connectivity
- [x] T046 [US1] Implement chi router setup in `internal/adapter/inbound/http/router.go` — mount token endpoint (with JWT middleware), WOPI routes (with auth + proof middlewares), health endpoint, discovery endpoint

**Checkpoint**: User Story 1 fully functional — token issuance + CheckFileInfo/GetFile/PutFile work end-to-end

---

## Phase 4: User Story 2 — Document Locking for Concurrent Editing (Priority: P2)

**Goal**: Lock/Unlock/RefreshLock/UnlockAndRelock with conflict detection

**Independent Test**: Send lock operations with matching/mismatching lock IDs, verify state transitions and 409 responses

### Tests for User Story 2

- [x] T047 [P] [US2] Write unit tests for lock operations in `internal/domain/service/wopi_service_test.go` — Lock (acquire, conflict, same-ID-refresh), Unlock (match, mismatch), RefreshLock, UnlockAndRelock (atomic swap)
- [x] T048 [P] [US2] Write integration test for lock endpoint handlers in `internal/adapter/inbound/http/wopi_handler_test.go` — all lock operations via HTTP with correct headers

### Implementation for User Story 2

- [x] T049 [US2] Add lock operations to WOPIService in `internal/domain/service/wopi_service.go` — Lock, Unlock, RefreshLock (+30min), UnlockAndRelock (atomic)
- [x] T050 [US2] Add lock endpoint handlers to `internal/adapter/inbound/http/wopi_handler.go` — dispatch POST on X-WOPI-Override (LOCK/UNLOCK/REFRESH_LOCK), distinguish Lock from UnlockAndRelock via X-WOPI-OldLock
- [x] T051 [US2] Add lock conflict to PutFile in `internal/domain/service/wopi_service.go` — verify X-WOPI-Lock matches before saving; 409 on mismatch

**Checkpoint**: User Stories 1 AND 2 both work — concurrent editing safely coordinated

---

## Phase 5: User Story 3 — WOPI Discovery for Collabora Integration (Priority: P3)

**Goal**: Discovery endpoint returning supported file types and editor URLs from Collabora

**Independent Test**: Query discovery endpoint, verify valid response; test cache when Collabora unreachable

### Tests for User Story 3

- [x] T052 [P] [US3] Write unit tests for DiscoveryService in `internal/domain/service/discovery_service_test.go` — fetch + cache, expiry, fallback, 503 when no cache
- [x] T053 [P] [US3] Write integration test for discovery handler in `internal/adapter/inbound/http/discovery_handler_test.go` — 200 with data, 503 when unavailable

### Implementation for User Story 3

- [x] T054 [US3] Implement DiscoveryService in `internal/domain/service/discovery_service.go` — cache discovery data (12-24h TTL), refresh on proof key failure, fallback to cache
- [x] T055 [US3] Implement discovery handler in `internal/adapter/inbound/http/discovery_handler.go` — GET /wopi/discovery returns JSON
- [x] T056 [US3] Wire discovery routes into router and integrate proof key cache into `middleware_proof.go`

**Checkpoint**: All user stories independently functional

---

## Phase 6: Polish & Cross-Cutting Concerns

- [x] T057 [P] Implement expired token/lock cleanup goroutine in `internal/domain/service/cleanup_service.go` — periodic deletion of expired tokens and locks
- [x] T058 [P] Create GitHub workflow `build-push-ghcr-pr.yml` based on matrix-adapter-go pattern
- [x] T059 [P] Create GitHub workflow `build-deploy-k8s-dev-hetzner.yml` — deploy to K8s dev on push to develop
- [x] T060 [P] Create GitHub workflow `build-release-docker-hub.yml` — DockerHub publish on release tag
- [x] T061 [P] Create GitHub workflows `build-deploy-k8s-test-hetzner.yml` and `build-deploy-k8s-sandbox-hetzner.yml`
- [x] T062 Run `golangci-lint run` across entire codebase and fix violations
- [x] T063 Run quickstart.md validation end-to-end

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies
- **Foundational (Phase 2)**: Depends on Setup — BLOCKS all user stories
- **User Stories (Phase 3+)**: All depend on Foundational
  - US2 extends US1 — recommended after US1
  - US3 is fully independent of US1/US2
- **Polish (Phase 6)**: Depends on all user stories

### Within Each User Story

- Tests MUST be written and FAIL before implementation
- Models/ports before services
- Services before handlers
- Core implementation before integration

### Parallel Opportunities

- Setup: T003, T004, T005 in parallel
- Domain model: T010–T014 in parallel
- Port definitions: T015–T021 in parallel
- SQL queries: T022–T024 in parallel
- DB adapters: T026–T028 in parallel (after T025)
- External adapters: T029–T032 in parallel
- Within each user story: tests [P] in parallel
- CI/CD workflows: T058–T061 in parallel

---

## Implementation Strategy

### MVP First (User Story 1 Only)

1. Phase 1: Setup
2. Phase 2: Foundational (CRITICAL)
3. Phase 3: User Story 1
4. **STOP and VALIDATE**: Token issuance + CheckFileInfo/GetFile/PutFile end-to-end
5. Deploy/demo if ready

### Incremental Delivery

1. Setup + Foundational → Foundation ready
2. User Story 1 → MVP!
3. User Story 2 → Locking
4. User Story 3 → Discovery
5. CI/CD → Production-ready

---

## Notes

- [P] = different files, no dependencies
- [Story] label maps task to user story
- Run `golangci-lint run` after completing each file
- All dependency versions verified online before pinning
- Use `actorId` internally, never `userId`
