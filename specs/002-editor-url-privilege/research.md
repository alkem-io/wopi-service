# Research: Editor URL, MIME Mapping, and Privilege Alignment

**Date**: 2026-04-14
**Feature**: 002-editor-url-privilege

## 1. WOPI Discovery URL Template Processing

**Decision**: Process `urlsrc` templates per WOPI spec
**Rationale**: Collabora's discovery XML contains `urlsrc` with
placeholders like `<ui=UI_LLCC&>`. Per the WOPI spec:
- Known placeholders: remove angle brackets, replace value, keep `&`
- Unknown placeholders: remove entirely including angle brackets
- `WOPISrc` is mandatory — append as query parameter
**Source**: Microsoft WOPI discovery documentation

## 2. MIME-to-Extension Mapping

**Decision**: Static lookup table in domain model
**Rationale**: MIME types are standardized and stable. A Go map
provides O(1) lookup. Covers the most common office document types:

| MIME Type | Extension |
|-----------|-----------|
| application/vnd.openxmlformats-officedocument.wordprocessingml.document | docx |
| application/msword | doc |
| application/vnd.oasis.opendocument.text | odt |
| application/vnd.openxmlformats-officedocument.spreadsheetml.sheet | xlsx |
| application/vnd.ms-excel | xls |
| application/vnd.oasis.opendocument.spreadsheet | ods |
| application/vnd.openxmlformats-officedocument.presentationml.presentation | pptx |
| application/vnd.ms-powerpoint | ppt |
| application/vnd.oasis.opendocument.presentation | odp |
| application/pdf | pdf |
| text/plain | txt |
| text/csv | csv |

## 3. URL Host Replacement

**Decision**: Replace Collabora internal host with WOPI_BASE_URL
**Rationale**: Discovery `urlsrc` contains the internal Collabora
URL (e.g., `http://collabora:9980/browser/...`). For the public
`editorUrl`, the host must be replaced with `WOPI_BASE_URL` since
the ingress routes `/browser/*` to Collabora.
**Approach**: Parse the `urlsrc`, extract the path+query, prepend
`WOPI_BASE_URL`.

## 4. Privilege Alignment

**Decision**: Keep `update-content` as-is
**Rationale**: Verified in authorization-evaluation-service that both
`update` and `update-content` exist as separate privileges.
`update-content` is specifically for content modifications, which is
exactly what WOPI document editing does. No change needed.
