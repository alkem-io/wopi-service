# WOPI Endpoint Contracts

**Date**: 2026-03-30 (revised)
**Feature**: 001-wopi-service-init

## Base Path

WOPI protocol endpoints are under `/wopi/files/{file_id}`.
Access token is passed as query parameter: `?access_token=<token>`.

Token issuance endpoint is under `/wopi/token` (behind Oathkeeper).

---

## Token Issuance (behind Oathkeeper)

```
POST /wopi/token
```

**Request headers** (injected by Oathkeeper):
- `Authorization: Bearer <JWT>` — contains `alkemio_actor_id` claim

**Request body** `Content-Type: application/json`:
```json
{
  "documentId": "<alkemio-document-uuid>"
}
```

**Response** `200 OK`:
```json
{
  "accessToken": "<url-safe-base64-opaque-token>",
  "accessTokenTTL": 1711814400000,
  "wopiSrc": "https://wopi.example.com/wopi/files/<document-id>"
}
```

- `accessTokenTTL`: UNIX timestamp in milliseconds (token expiry)
- `wopiSrc`: Full WOPI file URL for Collabora iframe construction

**Flow**:
1. Extract `alkemio_actor_id` from JWT
2. Look up document in Alkemio DB (get authorizationPolicyId)
3. Call NATS `auth.evaluate` with `{actorId, privilege: "read", authorizationPolicyId}`
4. If authorized with `update-content`, set permissions to "read,write"
5. Generate opaque token, store in DB, return

**Status codes**: 200, 401 (no/invalid JWT), 403 (not authorized), 404 (document not found), 503 (NATS unavailable)

---

## CheckFileInfo

```
GET /wopi/files/{file_id}?access_token=<token>
```

**Response** `200 OK` `Content-Type: application/json`:

```json
{
  "BaseFileName": "report.docx",
  "OwnerId": "alkemio-actor-id",
  "Size": 45678,
  "UserId": "alkemio-actor-id",
  "Version": "v1-20260330T120000Z",
  "UserFriendlyName": "Jane Doe",
  "UserCanWrite": true,
  "SupportsLocks": true,
  "SupportsUpdate": true,
  "LastModifiedTime": "2026-03-30T12:00:00Z",
  "UserCanNotWriteRelative": true,
  "PostMessageOrigin": "https://alkemio.example.com"
}
```

**Data sources**:
- `BaseFileName`, `Size`, `OwnerId` → Alkemio DB (document table)
- `UserId`, `UserCanWrite` → from access token record
- `Version` → derived from document externalID or last modified time

**Status codes**: 200, 401, 404, 500

---

## GetFile

```
GET /wopi/files/{file_id}/contents?access_token=<token>
```

**Flow**: Read file from file-service-go `GET /internal/storage/:externalID`

**Response** `200 OK`: Raw binary file content.
**Response header**: `X-WOPI-ItemVersion` (optional)

**Status codes**: 200, 401, 404, 500, 502 (file-service unavailable)

---

## PutFile

```
POST /wopi/files/{file_id}/contents?access_token=<token>
```

**Request headers**:
- `X-WOPI-Override: PUT`
- `X-WOPI-Lock: <lock_id>` (required if file is locked)

**Request body**: Full binary file content.

**Flow**: Write file via file-service-go
`PUT /internal/storage/document/:documentId` with binary body

**Response headers**:
- `X-COOL-WOPI-Timestamp: <ISO 8601>` (Collabora extension)
- `X-WOPI-ItemVersion` (optional)

**Conflict response** (external change detected):
- `409 Conflict` with body `{"COOLStatusCode": 1010}`

**Collabora informational request headers** (read-only, for logging):
- `X-COOL-WOPI-IsAutosave: true/false`
- `X-COOL-WOPI-IsModifiedByUser: true/false`

**Status codes**: 200, 401, 404, 409, 500, 502

---

## Lock

```
POST /wopi/files/{file_id}?access_token=<token>
```

**Request headers**:
- `X-WOPI-Override: LOCK`
- `X-WOPI-Lock: <new_lock_id>`

**Behavior**:
- If unlocked → acquire lock, return 200
- If locked with same ID → treat as RefreshLock, return 200
- If locked with different ID → return 409

**Response headers** (on 409):
- `X-WOPI-Lock: <current_lock_id>`

**Status codes**: 200, 401, 404, 409, 500

---

## Unlock

```
POST /wopi/files/{file_id}?access_token=<token>
```

**Request headers**:
- `X-WOPI-Override: UNLOCK`
- `X-WOPI-Lock: <lock_id>`

**Behavior**:
- If lock matches → release, return 200
- If mismatch or unlocked → return 409

**Response headers** (on 409):
- `X-WOPI-Lock: <current_lock_id>` (empty string if unlocked)

**Status codes**: 200, 401, 404, 409, 500

---

## RefreshLock

```
POST /wopi/files/{file_id}?access_token=<token>
```

**Request headers**:
- `X-WOPI-Override: REFRESH_LOCK`
- `X-WOPI-Lock: <lock_id>`

**Behavior**: Reset lock expiry to +30 minutes. 409 on mismatch.

**Status codes**: 200, 401, 404, 409, 500

---

## UnlockAndRelock

```
POST /wopi/files/{file_id}?access_token=<token>
```

**Request headers**:
- `X-WOPI-Override: LOCK`
- `X-WOPI-Lock: <new_lock_id>`
- `X-WOPI-OldLock: <old_lock_id>` (distinguishes from Lock)

**Behavior**: Atomic. Verify OldLock matches → replace with new
lock ID. 409 on mismatch.

**Status codes**: 200, 401, 404, 409, 500

---

## Routing Note

Lock, Unlock, RefreshLock, and UnlockAndRelock all share the same
path `POST /wopi/files/{file_id}`. The handler MUST dispatch based on:

1. `X-WOPI-Override` header value
2. Presence of `X-WOPI-OldLock` header (to distinguish Lock from
   UnlockAndRelock when Override is `LOCK`)

---

## Common Request Headers (all WOPI endpoints)

| Header                  | Purpose                        |
|-------------------------|--------------------------------|
| `X-WOPI-Proof`         | Proof key signature (current)  |
| `X-WOPI-ProofOld`      | Proof key signature (old)      |
| `X-WOPI-TimeStamp`     | .NET ticks for proof validation|
| `X-WOPI-SessionId`     | Logging correlation            |
| `X-WOPI-CorrelationId` | Logging correlation            |

---

## Health Check

```
GET /health
```

**Response** `200 OK`:
```json
{"status": "ok"}
```

---

## Discovery Proxy

```
GET /wopi/discovery
```

**Response** `200 OK`: Parsed discovery data (supported file types
and editor action URLs from Collabora).

**Response** `503 Service Unavailable`: Collabora unreachable and
no cached data.

**Cache**: Discovery XML cached for 12-24 hours. Re-fetched on
proof key validation failure.
