# Feature Specification: Initial WOPI Service Implementation

**Feature Branch**: `001-wopi-service-init`
**Created**: 2026-03-30
**Status**: Draft
**Input**: User description: "write initial implementation of the WOPI service"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Open Document for Editing in Collabora (Priority: P1)

A user in the Alkemio platform clicks to edit a document. The system generates a WOPI-compatible URL and access token, then launches Collabora Online with that URL. Collabora calls CheckFileInfo and GetFile on this WOPI service to load the document. The user edits the document in Collabora and saves it; Collabora calls PutFile on the WOPI service, which persists the updated content back through Alkemio's storage layer.

**Why this priority**: This is the core value proposition — without document open/view/edit/save, the service has no purpose. All other features depend on this flow working.

**Independent Test**: Can be fully tested by issuing WOPI CheckFileInfo, GetFile, and PutFile requests with a valid access token and verifying correct responses and storage updates.

**Acceptance Scenarios**:

1. **Given** a valid access token for a document the user has edit access to, **When** Collabora sends a CheckFileInfo request, **Then** the service returns file metadata (filename, size, user info, permissions) with a 200 status.
2. **Given** a valid access token for a document, **When** Collabora sends a GetFile request, **Then** the service returns the file content as a binary stream with a 200 status.
3. **Given** a valid access token with edit permission, **When** Collabora sends a PutFile request with updated content, **Then** the service persists the new content through Alkemio's storage and returns a 200 status.
4. **Given** an invalid or expired access token, **When** Collabora sends any WOPI request, **Then** the service returns a 401 status.

---

### User Story 2 - Document Locking for Concurrent Editing (Priority: P2)

When a user opens a document for editing, Collabora requests a lock to prevent conflicting edits. The WOPI service manages lock state so that concurrent editing sessions are coordinated. Locks can be acquired, refreshed, and released.

**Why this priority**: Locking prevents data loss from concurrent edits. Without it, the last save wins and earlier edits are silently lost.

**Independent Test**: Can be tested by sending Lock, RefreshLock, Unlock, and UnlockAndRelock WOPI requests and verifying lock state transitions and conflict responses.

**Acceptance Scenarios**:

1. **Given** no existing lock on a document, **When** Collabora sends a Lock request, **Then** the service acquires the lock and returns a 200 status.
2. **Given** an existing lock with a matching lock ID, **When** Collabora sends a RefreshLock request, **Then** the lock expiry is extended and the service returns a 200 status.
3. **Given** an existing lock with a matching lock ID, **When** Collabora sends an Unlock request, **Then** the lock is released and the service returns a 200 status.
4. **Given** an existing lock with a different lock ID, **When** Collabora sends a Lock request, **Then** the service returns a 409 status with the current lock ID in the response header.

---

### User Story 3 - WOPI Discovery for Collabora Integration (Priority: P3)

An administrator or the Alkemio platform queries the WOPI discovery endpoint to determine which file types are supported and how to construct editor URLs. The WOPI service proxies or caches the Collabora discovery XML and provides it in a consumable format.

**Why this priority**: Discovery is required for the platform to know which file extensions Collabora supports and how to build editor URLs. It is needed for the full integration but the core editing flow (US1) can be manually configured without it initially.

**Independent Test**: Can be tested by requesting the discovery endpoint and verifying it returns valid discovery data with supported MIME types and editor action URLs.

**Acceptance Scenarios**:

1. **Given** Collabora Online is reachable, **When** the discovery endpoint is queried, **Then** the service returns a list of supported file types with their associated editor URLs.
2. **Given** Collabora Online is temporarily unreachable, **When** the discovery endpoint is queried, **Then** the service returns cached discovery data if available, or a 503 status with a meaningful error if no cache exists.

---

### Edge Cases

- What happens when PutFile is called but the access token has expired mid-editing session?
- How does the system handle a Lock request for a document that has been deleted from storage?
- What happens when Collabora sends a request with an invalid or missing WOPI proof signature?
- How does the system behave when Alkemio's storage service is unavailable during GetFile or PutFile?

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST expose WOPI REST endpoints at `/wopi/files/{file_id}` and `/wopi/files/{file_id}/contents` following the WOPI protocol specification.
- **FR-002**: System MUST validate access tokens on every WOPI request and reject unauthorized requests with a 401 status.
- **FR-003**: System MUST validate WOPI proof signatures from Collabora on every request to confirm request authenticity.
- **FR-004**: System MUST implement CheckFileInfo returning file metadata (filename, size, owner, user permissions, supported WOPI features) fetched from Alkemio via the RabbitMQ INFO pattern on each call.
- **FR-005**: System MUST implement GetFile returning binary file content retrieved from Alkemio's storage layer.
- **FR-006**: System MUST implement PutFile persisting updated file content back to Alkemio's storage layer.
- **FR-007**: System MUST implement Lock, Unlock, RefreshLock, and UnlockAndRelock operations with lock state tracked in the database.
- **FR-008**: System MUST generate opaque, time-limited access tokens stored in PostgreSQL, encoding file ID, user ID, and permission scope. Tokens are validated via DB lookup on each request.
- **FR-009**: System MUST provide a discovery endpoint that returns supported file types and editor URLs from Collabora.
- **FR-010**: System MUST authenticate users via Alkemio's JWT/Kratos token validation before issuing WOPI access tokens.
- **FR-011**: System MUST authorize file access by delegating to Alkemio's authorization policy engine.
- **FR-012**: System MUST persist lock state (lock ID, file ID, expiry) in PostgreSQL.
- **FR-013**: System MUST expose a health check endpoint for infrastructure monitoring.

### Key Entities

- **File**: Not stored locally. File metadata is fetched from Alkemio via RabbitMQ (INFO) on each CheckFileInfo request. Alkemio is the single source of truth. Referenced by: Alkemio external ID, filename, size, owner ID, last modified timestamp.
- **AccessToken**: An opaque, DB-backed token granting a specific user specific permissions on a specific file. Validated via database lookup on each request. Default TTL: 8 hours. Key attributes: token value, file ID, user ID, permissions, expiry.
- **Lock**: Represents an active edit lock on a file. Default expiry: 30 minutes, extended by RefreshLock. Key attributes: lock ID, file ID, created timestamp, expiry timestamp.
- **WOPISession**: Tracks an active editing session. Links a user, file, and access token. Key attributes: session ID, user ID, file ID, token reference, created timestamp.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can open a document from Alkemio in Collabora Online, edit it, and save changes that are persisted back to Alkemio storage — full round-trip works end to end.
- **SC-002**: Unauthorized requests (invalid token, expired token, insufficient permissions) are rejected and never return file content.
- **SC-003**: Concurrent edit attempts on the same document are mediated by locks — the second editor receives a lock conflict response rather than silently overwriting.
- **SC-004**: The service starts, connects to its database, and responds to health checks without manual intervention.
- **SC-005**: Discovery data is available to the platform, enabling automatic editor URL construction for supported file types.

## Clarifications

### Session 2026-03-30

- Q: How does the WOPI service communicate with Alkemio Server for authorization and storage? → A: RabbitMQ messages (via Watermill) using Alkemio's existing collaborative-document patterns (INFO, WHO, SAVE, FETCH).
- Q: What format should WOPI access tokens use? → A: Opaque tokens stored in PostgreSQL, looked up on each request.
- Q: What should the default lock expiry duration be? → A: 30 minutes (Collabora refreshes every ~15 minutes).
- Q: Does the WOPI service maintain its own file metadata table? → A: No. File metadata is fetched from Alkemio via RabbitMQ (INFO) on each CheckFileInfo call. Alkemio is the single source of truth.
- Q: What access token TTL should be used? → A: 8 hours (typical working day session).

## Assumptions

- Alkemio Server communicates with this service via RabbitMQ using the existing collaborative-document-integration message patterns (INFO, WHO, SAVE, FETCH). No REST APIs are used for auth or storage operations between these services.
- Collabora Online is deployed alongside this service and is network-reachable.
- PostgreSQL is available as a managed or self-hosted instance for this service's own state (locks, sessions, tokens).
- The WOPI service runs as a standalone HTTP server behind the same reverse proxy / API gateway as the Alkemio platform.
- Initial scope covers Collabora Online as the only WOPI client; other WOPI clients may be added later but are not in scope now.
- File content is not cached by the WOPI service — every GetFile reads from Alkemio storage, and every PutFile writes to it.
