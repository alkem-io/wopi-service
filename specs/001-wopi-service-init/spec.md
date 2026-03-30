# Feature Specification: Initial WOPI Service Implementation

**Feature Branch**: `001-wopi-service-init`
**Created**: 2026-03-30
**Status**: Draft
**Input**: User description: "write initial implementation of the WOPI service"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Open Document for Editing in Collabora (Priority: P1)

A user in the Alkemio platform clicks to edit a document. The frontend
requests a WOPI access token from this service (the request passes
through Oathkeeper, which injects the actor's identity). The service
checks authorization via NATS auth-evaluation-service, generates an
opaque access token, and returns it along with the WOPI source URL.
The frontend constructs a Collabora editor URL and opens it.

Collabora calls CheckFileInfo and GetFile on this WOPI service to load
the document (using the opaque access token). The service looks up
document metadata from Alkemio's DB and retrieves file content from
file-service-go. The user edits and saves; Collabora calls PutFile,
and the service writes the updated content via file-service-go's
store-and-link endpoint.

**Why this priority**: This is the core value proposition — without
document open/view/edit/save, the service has no purpose.

**Independent Test**: Can be tested by requesting a WOPI token, then
issuing CheckFileInfo, GetFile, and PutFile requests with that token
and verifying correct responses and storage updates.

**Acceptance Scenarios**:

1. **Given** a valid Oathkeeper JWT and a document the actor has edit access to, **When** a token issuance request is sent, **Then** the service returns an opaque access token, TTL, and WOPI source URL.
2. **Given** a valid access token, **When** Collabora sends a CheckFileInfo request, **Then** the service returns file metadata (filename, size, user info, permissions) with a 200 status.
3. **Given** a valid access token, **When** Collabora sends a GetFile request, **Then** the service returns the file content from file-service-go with a 200 status.
4. **Given** a valid access token with edit permission, **When** Collabora sends a PutFile request, **Then** the service writes the content via file-service-go and returns a 200 status.
5. **Given** an invalid or expired access token, **When** Collabora sends any WOPI request, **Then** the service returns a 401 status.
6. **Given** a valid Oathkeeper JWT but the actor lacks permission, **When** a token issuance request is sent, **Then** the service returns 403.

---

### User Story 2 - Document Locking for Concurrent Editing (Priority: P2)

When a user opens a document for editing, Collabora requests a lock to
prevent conflicting edits. The WOPI service manages lock state so that
concurrent editing sessions are coordinated. Locks can be acquired,
refreshed, and released.

**Why this priority**: Locking prevents data loss from concurrent
edits. Without it, the last save wins and earlier edits are silently
lost.

**Independent Test**: Can be tested by sending Lock, RefreshLock,
Unlock, and UnlockAndRelock WOPI requests and verifying lock state
transitions and conflict responses.

**Acceptance Scenarios**:

1. **Given** no existing lock on a document, **When** Collabora sends a Lock request, **Then** the service acquires the lock and returns a 200 status.
2. **Given** an existing lock with a matching lock ID, **When** Collabora sends a RefreshLock request, **Then** the lock expiry is extended and the service returns a 200 status.
3. **Given** an existing lock with a matching lock ID, **When** Collabora sends an Unlock request, **Then** the lock is released and the service returns a 200 status.
4. **Given** an existing lock with a different lock ID, **When** Collabora sends a Lock request, **Then** the service returns a 409 status with the current lock ID in the response header.

---

### User Story 3 - WOPI Discovery for Collabora Integration (Priority: P3)

An administrator or the Alkemio platform queries the WOPI discovery
endpoint to determine which file types are supported and how to
construct editor URLs. The WOPI service proxies or caches the Collabora
discovery XML and provides it in a consumable format.

**Why this priority**: Discovery is required for the platform to know
which file extensions Collabora supports and how to build editor URLs.
It is needed for the full integration but the core editing flow (US1)
can be manually configured without it initially.

**Independent Test**: Can be tested by requesting the discovery
endpoint and verifying it returns valid discovery data with supported
MIME types and editor action URLs.

**Acceptance Scenarios**:

1. **Given** Collabora Online is reachable, **When** the discovery endpoint is queried, **Then** the service returns a list of supported file types with their associated editor URLs.
2. **Given** Collabora Online is temporarily unreachable, **When** the discovery endpoint is queried, **Then** the service returns cached discovery data if available, or a 503 status with a meaningful error if no cache exists.

---

### Edge Cases

- What happens when PutFile is called but the access token has expired mid-editing session? → 401 Unauthorized per WOPI spec.
- How does the system handle a Lock request for a document that has been deleted from storage? → 404 Not Found.
- What happens when Collabora sends a request with an invalid or missing WOPI proof signature? → 401 Unauthorized.
- How does the system behave when file-service-go is unavailable during GetFile or PutFile? → 502 Bad Gateway.
- What happens when NATS (auth-evaluation-service) is unavailable during token issuance? → 503 Service Unavailable.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST expose WOPI REST endpoints at `/wopi/files/{file_id}` and `/wopi/files/{file_id}/contents` following the WOPI protocol specification.
- **FR-002**: System MUST validate access tokens on every WOPI request and reject unauthorized requests with a 401 status.
- **FR-003**: System MUST validate WOPI proof signatures from Collabora on every request to confirm request authenticity.
- **FR-004**: System MUST implement CheckFileInfo returning file metadata (filename, size, owner, user permissions, supported WOPI features) looked up from Alkemio's PostgreSQL database.
- **FR-005**: System MUST implement GetFile returning binary file content retrieved from file-service-go's private read endpoint.
- **FR-006**: System MUST implement PutFile persisting updated file content via file-service-go's store-and-link endpoint (`PUT /internal/storage/document/:documentId`).
- **FR-007**: System MUST implement Lock, Unlock, RefreshLock, and UnlockAndRelock operations with lock state tracked in the database.
- **FR-008**: System MUST generate opaque, time-limited access tokens stored in PostgreSQL, encoding file ID, actor ID, and permission scope. Tokens are validated via DB lookup on each request.
- **FR-009**: System MUST provide a discovery endpoint that returns supported file types and editor URLs from Collabora.
- **FR-010**: System MUST expose a token issuance endpoint (`POST /wopi/token`) behind Oathkeeper that accepts a document ID, extracts actor identity from the Oathkeeper-injected JWT (`alkemio_actor_id` claim), checks authorization via NATS auth-evaluation-service, and returns an opaque WOPI access token with TTL and WOPI source URL.
- **FR-011**: System MUST authorize file access by calling the authorization-evaluation-service via NATS (`auth.evaluate` subject) with agentId, privilege (`read` or `update-content`), and the document's authorizationPolicyId.
- **FR-012**: System MUST look up document metadata (externalID, authorizationPolicyId, mimeType, displayName, size) from Alkemio's PostgreSQL database using a read-only connection.
- **FR-013**: System MUST persist lock state (lock ID, file ID, expiry) in its own PostgreSQL database.
- **FR-014**: System MUST expose a health check endpoint for infrastructure monitoring.

### Key Entities

- **Document** (Alkemio DB, read-only): File metadata in Alkemio. Key attributes: id (UUID), externalID (SHA3-256 hash), displayName, mimeType, size, authorizationPolicyId, storageBucket. Looked up to resolve file info and authorization policy.
- **AccessToken**: An opaque, DB-backed token granting a specific actor specific permissions on a specific file. Validated via database lookup on each request. Default TTL: 8 hours. Key attributes: token value, file ID, actor ID, permissions, expiry.
- **Lock**: Represents an active edit lock on a file. Default expiry: 30 minutes, extended by RefreshLock. Key attributes: lock ID, file ID, created timestamp, expiry timestamp.
- **WOPISession**: Tracks an active editing session. Links an actor, file, and access token. Key attributes: session ID, actor ID, file ID, token reference, created timestamp.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can open a document from Alkemio in Collabora Online, edit it, and save changes that are persisted back via file-service-go — full round-trip works end to end.
- **SC-002**: Unauthorized requests (invalid token, expired token, insufficient permissions) are rejected and never return file content.
- **SC-003**: Concurrent edit attempts on the same document are mediated by locks — the second editor receives a lock conflict response rather than silently overwriting.
- **SC-004**: The service starts, connects to its database, NATS, and file-service-go, and responds to health checks without manual intervention.
- **SC-005**: Discovery data is available to the platform, enabling automatic editor URL construction for supported file types.

## Clarifications

### Session 2026-03-30

- Q: How does the WOPI service communicate with Alkemio for authorization? → A: NATS call to authorization-evaluation-service (`auth.evaluate`) with agentId, privilege, and authorizationPolicyId. No RabbitMQ.
- Q: How does the WOPI service authenticate the user for token issuance? → A: Oathkeeper injects JWT with `alkemio_actor_id` claim. No WHO RabbitMQ pattern needed.
- Q: How does the WOPI service read/write files? → A: Via file-service-go private endpoints. GetFile uses `GET /internal/storage/:externalID`. PutFile uses `PUT /internal/storage/document/:documentId`.
- Q: Where does the WOPI service get document metadata? → A: Read-only access to Alkemio's PostgreSQL database (document table).
- Q: What format should WOPI access tokens use? → A: Opaque tokens stored in PostgreSQL, looked up on each request.
- Q: What should the default lock expiry duration be? → A: 30 minutes (Collabora refreshes every ~15 minutes).
- Q: What access token TTL should be used? → A: 8 hours (typical working day session).

## Assumptions

- Oathkeeper is configured to route the WOPI token issuance endpoint and inject JWTs with `alkemio_actor_id` claim.
- WOPI protocol endpoints (called by Collabora) are NOT behind Oathkeeper — they use opaque access tokens directly.
- The authorization-evaluation-service is deployed and reachable via NATS at `auth.evaluate`.
- Alkemio's PostgreSQL database is accessible with a read-only user for document metadata lookups.
- file-service-go is deployed and its private endpoints are reachable within the K8s cluster.
- Collabora Online is deployed alongside this service and is network-reachable.
- PostgreSQL is available for this service's own state (locks, sessions, tokens).
- The WOPI service runs as a standalone HTTP server behind the same reverse proxy / API gateway as the Alkemio platform.
- Initial scope covers Collabora Online as the only WOPI client.
- File content is not cached by the WOPI service — every GetFile reads from file-service-go, and every PutFile writes to it.
