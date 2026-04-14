# Tasks: Editor URL, MIME Mapping, and Privilege Alignment

**Input**: Design documents from `/specs/002-editor-url-privilege/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included — constitution mandates test-first development.

**Organization**: Tasks grouped by user story. US2 (MIME mapping) is a prerequisite for US1 (editor URL). US3 (privilege alignment) is verification only.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story (US1, US2, US3)

---

## Phase 1: Foundational

**Purpose**: MIME mapping table and discovery action lookup — required by US1 and US2

- [x] T001 Create MIME-to-extension mapping table in `internal/domain/model/mime.go` — static `map[string]string` covering docx, doc, odt, xlsx, xls, ods, pptx, ppt, odp, pdf, txt, csv with a `ExtensionForMIME(mimeType string) (string, error)` function
- [x] T002 [P] Write unit tests for MIME mapping in `internal/domain/model/mime_test.go` — test all mapped types, unmapped type returns error
- [x] T003 Add `FindActionByExtension(ext string, preferEdit bool) (*DiscoveryAction, error)` method to DiscoveryService in `internal/domain/service/discovery_service.go` — looks up cached discovery actions by extension, prefers "edit" action when `preferEdit=true`, falls back to "view"
- [x] T004 [P] Write unit tests for FindActionByExtension in `internal/domain/service/discovery_service_test.go` — test edit preferred, view fallback, unknown extension returns error

**Checkpoint**: MIME mapping and discovery action lookup work independently

---

## Phase 2: User Story 1 — Ready-to-Use Editor URL in Token Response (Priority: P1)

**Goal**: `POST /wopi/token` response includes `editorUrl` field

**Independent Test**: Request token for .docx, verify editorUrl contains correct Collabora editor path, WOPISrc, access_token, access_token_ttl

### Tests for User Story 1

- [x] T005 [P] [US1] Write unit tests for editor URL construction in `internal/domain/service/token_service_test.go` — test editorUrl contains WOPI_BASE_URL, correct editor path, URL-encoded WOPISrc, access_token, access_token_ttl; test unsupported MIME returns 422; test empty discovery cache returns error

### Implementation for User Story 1

- [x] T006 [US1] Add `buildEditorURL` function to `internal/domain/service/token_service.go` — takes discovery action urlsrc, baseURL, wopiSrc, accessToken, ttl; replaces Collabora internal host with WOPI_BASE_URL; processes urlsrc template placeholders per WOPI spec; appends WOPISrc, access_token, access_token_ttl query parameters
- [x] T007 [US1] Extend `IssueToken` in `internal/domain/service/token_service.go` — after token creation, resolve MIME→extension→discovery action→editorUrl; add `EditorUrl` field to `TokenIssuanceResult`; return 422-equivalent error for unsupported MIME types
- [x] T008 [US1] Add `EditorUrl` field to `TokenIssuanceResponse` in `internal/adapter/inbound/http/dto.go`
- [x] T009 [US1] Update token handler error handling in `internal/adapter/inbound/http/token_handler.go` — map unsupported MIME error to 422 Unprocessable Entity

**Checkpoint**: Token response includes working editorUrl for supported document types

---

## Phase 3: User Story 2 — Document Type to Editor Mapping (Priority: P2)

**Goal**: Verify the correct Collabora app is selected for each document type

**Independent Test**: Already covered by Phase 1 (T002, T004) and Phase 2 (T005) — this story is the integration of MIME mapping + discovery action lookup tested end-to-end

- [x] T010 [US2] Write integration test in `internal/domain/service/token_service_test.go` — test IssueToken for .docx/.xlsx/.pptx/.odt documents each produces editorUrl pointing to the correct Collabora app (verify action name in URL)

**Checkpoint**: All document types resolve to correct editors

---

## Phase 4: User Story 3 — Write Privilege Alignment (Priority: P3)

**Goal**: Confirm update-content is the correct privilege (verification only)

- [x] T011 [US3] Write verification test in `internal/domain/service/token_service_test.go` — test that write token (update-content granted) produces editorUrl with "edit" action; read-only token produces editorUrl with "view" action

**Checkpoint**: Privilege alignment verified — no code change needed

---

## Phase 5: Polish

- [x] T012 Regenerate OpenAPI spec with `make openapi` and commit
- [x] T013 Run `golangci-lint run` and fix any violations
- [x] T014 Run full test suite and verify all tests pass

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1** (Foundational): No dependencies — MIME mapping and action lookup
- **Phase 2** (US1): Depends on Phase 1 — uses MIME mapping and action lookup
- **Phase 3** (US2): Depends on Phase 2 — integration tests
- **Phase 4** (US3): Independent — can run after Phase 2
- **Phase 5** (Polish): After all phases

### Parallel Opportunities

- T001 and T002 can run in parallel (different files)
- T003 and T004 can run in parallel
- T005 can run in parallel with T006 (test before implementation)
- T010 and T011 can run in parallel (different test scenarios)

---

## Implementation Strategy

### MVP (US1 + US2)

1. Phase 1: MIME mapping + discovery action lookup
2. Phase 2: Editor URL in token response
3. Phase 3: Integration verification
4. **STOP and VALIDATE**: Token response has working editorUrl

### Full Delivery

5. Phase 4: Privilege verification tests
6. Phase 5: Polish

---

## Notes

- No database migrations needed
- No new dependencies
- No config changes (WOPI_BASE_URL already exists)
- No router changes
- Run `golangci-lint run` after each file
- Commit after each phase
