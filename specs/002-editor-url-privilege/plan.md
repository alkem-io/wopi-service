# Implementation Plan: Editor URL, MIME Mapping, and Privilege Alignment

**Branch**: `002-editor-url-privilege` | **Date**: 2026-04-14 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/002-editor-url-privilege/spec.md`

## Summary

Extend the `POST /wopi/token` response with a ready-to-use `editorUrl`
field. The WOPI service resolves the correct Collabora editor by mapping
the document's MIME type → file extension → discovery action, then
constructs the full editor URL using `WOPI_BASE_URL`. Also confirms
that `update-content` is the correct write privilege (no change needed).

## Technical Context

**Language/Version**: Go 1.26 (existing codebase)
**Primary Dependencies**: No new dependencies — uses existing discovery service and config
**Storage**: No schema changes
**Testing**: Unit tests with mock discovery data
**Project Type**: Enhancement to existing web service
**Constraints**: Existing token issuance flow, existing discovery cache

## Constitution Check

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Hexagonal Architecture | PASS | New logic in domain service layer, no adapter changes |
| II. WOPI Protocol Compliance | PASS | Editor URL follows WOPI urlsrc template processing |
| VIII. DRY | PASS | MIME mapping is a single source of truth |
| IX. Lint on Completion | PASS | golangci-lint before commit |
| All others | PASS | No violations |

## Project Structure

### Files Modified

```text
internal/
├── domain/
│   ├── service/
│   │   ├── token_service.go        # Add editorUrl to IssueToken result
│   │   ├── token_service_test.go   # Test editor URL construction
│   │   ├── discovery_service.go    # Add FindActionByExtension method
│   │   └── discovery_service_test.go # Test action lookup
│   └── model/
│       └── mime.go                  # MIME-to-extension mapping table
├── adapter/
│   └── inbound/
│       └── http/
│           └── dto.go               # Add EditorUrl to TokenIssuanceResponse
```

### Files Unchanged

- No migration changes
- No new adapters
- No config changes (WOPI_BASE_URL already exists)
- No router changes

## Complexity Tracking

No constitution violations. No complexity justifications needed.
