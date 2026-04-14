# Feature Specification: Editor URL, MIME Mapping, and Privilege Alignment

**Feature Branch**: `002-editor-url-privilege`
**Created**: 2026-04-14
**Status**: Draft
**Input**: User description: "Extend WOPI token with editor URL, MIME-based editor mapping, and privilege alignment"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Ready-to-Use Editor URL in Token Response (Priority: P1)

A user in the Alkemio platform clicks to edit a document. The frontend
requests a WOPI access token from the WOPI service. The response now
includes a ready-to-use `editorUrl` path that the frontend can directly
load in an iframe — no URL construction needed on the client side.

The WOPI service resolves the correct Collabora editor URL by looking up
the document's MIME type and matching it against the cached WOPI
discovery data to find the appropriate editor action URL template. The
returned `editorUrl` uses `WOPI_BASE_URL` as the base domain (e.g.,
`https://sandbox-alkem.io`). The ingress routes `/browser/*` to
Collabora and `/wopi/*` to the WOPI service, so both the editor path
and WOPISrc share the same public domain.

**Why this priority**: This is the primary integration point — without
the editor URL, the frontend cannot open the Collabora editor.

**Independent Test**: Request a WOPI token for a .docx document and
verify the response includes a valid `editorUrl` path containing the
correct Collabora editor app, WOPISrc, and access token parameters.

**Acceptance Scenarios**:

1. **Given** a valid token issuance request for a .docx document, **When** the WOPI service responds, **Then** the response includes an `editorUrl` using `WOPI_BASE_URL` as base, pointing to the Writer editor with URL-encoded WOPISrc and access_token query parameters.
2. **Given** a valid token issuance request for a .xlsx document, **When** the WOPI service responds, **Then** the `editorUrl` points to the Calc editor.
3. **Given** a valid token issuance request for a .pptx document, **When** the WOPI service responds, **Then** the `editorUrl` points to the Impress editor.
4. **Given** a document with a MIME type not supported by Collabora, **When** a token is requested, **Then** the service returns an error indicating the document type is not supported for editing.
5. **Given** the discovery cache is empty and Collabora is unreachable, **When** a token is requested, **Then** the service returns a 503 error (cannot resolve editor URL).

---

### User Story 2 - Document Type to Editor Mapping (Priority: P2)

The WOPI service resolves which Collabora application (Writer, Calc,
Impress, Draw) handles a given document by matching the document's
MIME type to the file extension, then looking up the corresponding
editor action in the WOPI discovery data. This mapping is performed
server-side so the frontend and Alkemio server do not need to know
about Collabora's capabilities.

**Why this priority**: This is a prerequisite for US1 — the editor URL
cannot be constructed without knowing which editor handles which
document type.

**Independent Test**: Query the WOPI service with documents of different
MIME types and verify each resolves to the correct Collabora application.

**Acceptance Scenarios**:

1. **Given** a document with MIME type `application/vnd.openxmlformats-officedocument.wordprocessingml.document`, **When** the editor is resolved, **Then** the Writer editor is selected.
2. **Given** a document with MIME type `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`, **When** the editor is resolved, **Then** the Calc editor is selected.
3. **Given** a document with MIME type `application/vnd.oasis.opendocument.text`, **When** the editor is resolved, **Then** the Writer editor is selected.
4. **Given** a document with MIME type `image/png`, **When** the editor is resolved, **Then** the service reports that this MIME type is not supported for editing.

---

### User Story 3 - Write Privilege Alignment (Priority: P3)

The WOPI service currently checks the `update-content` privilege for
write permission. This aligns with the Alkemio authorization model
where `update-content` is a specific privilege for modifying document
content, distinct from the broader `update` privilege (which covers
metadata changes). No change is needed — `update-content` is the
correct privilege for WOPI document editing.

**Why this priority**: Verification only — confirms the current
implementation is correct. No code change required.

**Independent Test**: Verify that the authorization-evaluation-service
accepts `update-content` as a valid privilege and that the Alkemio
server assigns this privilege to actors who should be able to edit
documents.

**Acceptance Scenarios**:

1. **Given** an actor with `update-content` privilege on a document, **When** a WOPI token is requested, **Then** the token grants write permission.
2. **Given** an actor with only `update` privilege (but not `update-content`) on a document, **When** a WOPI token is requested, **Then** the token grants read-only permission.
3. **Given** an actor with only `read` privilege, **When** a WOPI token is requested, **Then** the token grants read-only permission.

---

### Edge Cases

- What happens when discovery data has multiple actions for the same extension (e.g., "edit" and "view")? → The "edit" action is preferred; "view" is used as fallback for read-only tokens.
- What happens when the document's file extension cannot be inferred from its MIME type? → Use a MIME-to-extension mapping table; return error if no mapping exists.
- What happens when the Collabora URL path changes between discovery refreshes while a user has an active session? → The editorUrl is generated at token issuance time and remains valid for the token's lifetime.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The `POST /wopi/token` response MUST include an `editorUrl` field containing a full URL to the Collabora editor, pre-populated with WOPISrc and access_token parameters.
- **FR-002**: The `editorUrl` MUST use `WOPI_BASE_URL` as the base for both the editor path and the WOPISrc parameter (e.g., `https://sandbox-alkem.io/browser/dist/cool.html?WOPISrc=https%3A%2F%2Fsandbox-alkem.io%2Fwopi%2Ffiles%2Fdoc-id&access_token=...`). The ingress routes `/browser/*` to Collabora and `/wopi/*` to the WOPI service.
- **FR-003**: The WOPI service MUST resolve the correct Collabora editor application based on the document's MIME type by mapping MIME type → file extension → discovery action.
- **FR-004**: The MIME-to-extension mapping MUST cover at minimum: `.docx`, `.doc`, `.odt`, `.xlsx`, `.xls`, `.ods`, `.pptx`, `.ppt`, `.odp`, `.pdf`, `.txt`, `.csv`.
- **FR-005**: When the document's MIME type is not supported by any Collabora editor, the token issuance endpoint MUST return a clear error (e.g., 422 Unprocessable Entity) indicating the document type is not supported.
- **FR-006**: The editor action lookup MUST prefer the "edit" action for tokens with write permission and the "view" action for read-only tokens.
- **FR-007**: The `editorUrl` MUST be constructed by processing the discovery action's `urlsrc` template (replacing placeholders per WOPI spec) and appending WOPISrc, access_token, and access_token_ttl (UNIX timestamp in milliseconds) parameters.
- **FR-008**: The WOPI service MUST continue to use `update-content` (not `update`) as the write privilege for document editing authorization checks.

### Key Entities

- **DiscoveryAction** (cached): Maps file extension to editor URL template. Key attributes: app name, action name (edit/view), extension, urlsrc template.
- **MIMEMapping**: Maps MIME types to file extensions for editor resolution. This is a static lookup table within the service.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The frontend can open a Collabora editor by loading the `editorUrl` from the token response directly in an iframe — no URL construction logic needed on the client side.
- **SC-002**: Documents of different types (.docx, .xlsx, .pptx, .odt) each open in the correct Collabora application (Writer, Calc, Impress).
- **SC-003**: Unsupported document types receive a clear error at token issuance time, not a broken editor URL.
- **SC-004**: The `editorUrl` uses `WOPI_BASE_URL` as base and works correctly when loaded in a browser iframe, with ingress routing `/browser/*` to Collabora and `/wopi/*` to the WOPI service.

## Clarifications

### Session 2026-04-14

- Q: Should access_token_ttl be included in the editorUrl? → A: Yes. Include access_token_ttl (UNIX ms) so Collabora can handle token expiry gracefully.

## Assumptions

- `WOPI_BASE_URL` (e.g., `https://sandbox-alkem.io`) is the shared public domain for Collabora and the WOPI service. Ingress routes `/browser/*`, `/hosting/discovery`, `/cool/*` to Collabora and `/wopi/*` to the WOPI service.
- `WOPI_COLLABORA_URL` (e.g., `http://collabora:9980`) is the internal cluster URL used only for fetching discovery data and internal communication.
- The discovery `urlsrc` templates from Collabora contain the internal URL. The service MUST replace the Collabora host portion with `WOPI_BASE_URL` when constructing the public `editorUrl`.
- The Collabora discovery cache is already implemented and provides the action URL templates.
- The `editorUrl` will be used by the frontend to construct an iframe src attribute.
- The `update-content` privilege is already assigned by the Alkemio server to actors who should be able to edit documents. No Alkemio server changes are needed.
- The MIME-to-extension mapping is static and maintained in the WOPI service code. If Collabora adds support for new file types, the mapping needs to be updated.
