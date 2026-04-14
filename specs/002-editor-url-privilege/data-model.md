# Data Model: Editor URL, MIME Mapping, and Privilege Alignment

**Date**: 2026-04-14
**Feature**: 002-editor-url-privilege

## Overview

No database schema changes. This feature adds in-memory domain types
only.

## New Types

### MIMEMapping (static, in-memory)

A Go `map[string]string` mapping MIME types to file extensions.
Defined in `internal/domain/model/mime.go`.

```text
"application/vnd.openxmlformats-officedocument.wordprocessingml.document" → "docx"
"application/msword" → "doc"
"application/vnd.oasis.opendocument.text" → "odt"
"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" → "xlsx"
"application/vnd.ms-excel" → "xls"
"application/vnd.oasis.opendocument.spreadsheet" → "ods"
"application/vnd.openxmlformats-officedocument.presentationml.presentation" → "pptx"
"application/vnd.ms-powerpoint" → "ppt"
"application/vnd.oasis.opendocument.presentation" → "odp"
"application/pdf" → "pdf"
"text/plain" → "txt"
"text/csv" → "csv"
```

### TokenIssuanceResult (extended)

Existing struct in `internal/domain/service/token_service.go`.
New field: `EditorURL string`.

## Unchanged

- `access_tokens` table — no changes
- `locks` table — no changes
- `wopi_sessions` table — no changes
- `DiscoveryData` / `DiscoveryAction` — already has extension,
  urlsrc, and action name fields
