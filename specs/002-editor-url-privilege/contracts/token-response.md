# Updated Token Issuance Response

**Date**: 2026-04-14
**Feature**: 002-editor-url-privilege

## POST /wopi/token — Updated Response

**Response** `200 OK`:
```json
{
  "accessToken": "<url-safe-base64-opaque-token>",
  "accessTokenTTL": 1711814400000,
  "wopiSrc": "https://sandbox-alkem.io/wopi/files/<document-id>",
  "editorUrl": "https://sandbox-alkem.io/browser/dist/cool.html?WOPISrc=https%3A%2F%2Fsandbox-alkem.io%2Fwopi%2Ffiles%2Fdoc-id&access_token=<token>&access_token_ttl=1711814400000"
}
```

### New field: `editorUrl`

- Full URL ready for iframe `src` attribute
- Base domain from `WOPI_BASE_URL`
- Editor path from Collabora discovery (`urlsrc` template processed)
- Query parameters:
  - `WOPISrc` — URL-encoded WOPI file URL
  - `access_token` — the opaque token
  - `access_token_ttl` — UNIX timestamp in milliseconds

### Editor Resolution

The `editorUrl` path varies by document type:

| Document | MIME Type | Extension | Collabora App | Typical Path |
|----------|-----------|-----------|---------------|-------------|
| Word | application/vnd.openxmlformats-...wordprocessingml.document | docx | Writer | /browser/dist/cool.html |
| Excel | application/vnd.openxmlformats-...spreadsheetml.sheet | xlsx | Calc | /browser/dist/cool.html |
| PowerPoint | application/vnd.openxmlformats-...presentationml.presentation | pptx | Impress | /browser/dist/cool.html |
| ODF Text | application/vnd.oasis.opendocument.text | odt | Writer | /browser/dist/cool.html |
| PDF | application/pdf | pdf | Writer (view) | /browser/dist/cool.html |

### Error responses

| Status | Condition |
|--------|-----------|
| 422 Unprocessable Entity | MIME type has no extension mapping, or extension has no matching editor action in discovery |
| 503 Service Unavailable | Discovery cache empty and Collabora unreachable |
