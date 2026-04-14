# Quickstart: Editor URL Feature

## Test the new editorUrl field

```bash
# Request a token (with Oathkeeper JWT)
curl -X POST https://sandbox-alkem.io/wopi/token \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{"documentId": "<document-uuid>"}'

# Expected response:
# {
#   "accessToken": "abc123...",
#   "accessTokenTTL": 1711814400000,
#   "wopiSrc": "https://sandbox-alkem.io/wopi/files/<doc-id>",
#   "editorUrl": "https://sandbox-alkem.io/browser/dist/cool.html?WOPISrc=...&access_token=...&access_token_ttl=..."
# }

# Open the editorUrl in a browser — Collabora editor loads directly
```

## Test MIME-to-editor mapping

Upload different document types and request tokens:
- `.docx` → Writer editor
- `.xlsx` → Calc editor
- `.pptx` → Impress editor
- `.odt` → Writer editor
- `.png` → 422 error (unsupported)

## Test privilege alignment

- Actor with `update-content` → token has write permission, editorUrl uses "edit" action
- Actor with only `read` → token is read-only, editorUrl uses "view" action
