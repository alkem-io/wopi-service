# Integration Service Contracts

**Date**: 2026-03-30
**Feature**: 001-wopi-service-init

## Overview

The WOPI service integrates with three external services. No RabbitMQ.

---

## 1. Authorization Evaluation Service (NATS)

**Subject**: `auth.evaluate`
**Protocol**: NATS request-reply
**Direction**: WOPI service → auth-evaluation-service

**Request**:
```json
{
  "pattern": "evaluate",
  "data": {
    "agentId": "<alkemio-actor-uuid>",
    "privilege": "read",
    "authorizationPolicyId": "<document-auth-policy-uuid>"
  }
}
```

**Privileges used by WOPI service**:
- `read` — required to view/download document
- `update-content` — required to edit document

**Response (granted)**:
```json
{
  "allowed": true,
  "reason": "Granted privilege 'read' using credential rule '...'"
}
```

**Response (denied)**:
```json
{
  "allowed": false,
  "reason": "Privilege 'update-content' not granted"
}
```

**Used by**: Token issuance endpoint (determine read/write
permissions before issuing WOPI access token).

---

## 2. File Service Go (HTTP, cluster-internal)

**Base URL**: configurable via `FILE_SERVICE_URL` env var
(e.g., `http://file-service-go:4003`)

### Read File

```
GET /internal/storage/:externalID
```

**Response**: Raw binary file content
**Status codes**: 200, 404

**Used by**: GetFile (stream content to Collabora)

### Write File + Update Document

```
PUT /internal/storage/document/:documentId
```

**Request body**: Raw binary file content
**Response**:
```json
{
  "externalID": "<sha3-256-hash>",
  "size": 45678
}
```

**Status codes**: 200, 404 (document not found), 500

**Used by**: PutFile (save edited content from Collabora, update
document record atomically)

### Check File Exists

```
HEAD /internal/storage/:externalID
```

**Status codes**: 200 (exists), 404 (not found)

---

## 3. Alkemio PostgreSQL Database (read-only)

**Connection**: Read-only user, separate connection pool from WOPI
service's own database.

### Document Lookup

Query the `document` table to resolve file metadata:

```sql
SELECT id, "externalID", "displayName", "mimeType", "size",
       "authorizationId"
FROM document
WHERE id = $1;
```

**Column details** (verified from Alkemio server migrations):
- `"authorizationId"` — UUID FK to `authorization_policy(id)`,
  camelCase column name (TypeORM convention). This is the value
  passed to auth-evaluation-service as `authorizationPolicyId`.
- `"externalID"` — SHA3-256 content hash, used as filename in
  storage. Passed to file-service-go for file reads.
- `"displayName"` — mapped to CheckFileInfo `BaseFileName`
- `"mimeType"` — enum, mapped to CheckFileInfo content type
- `"size"` — integer, file size in bytes

**Used by**: Token issuance (get authorizationPolicyId), CheckFileInfo
(get file metadata), GetFile (get externalID), PutFile (get
documentId for store-and-link).

---

## 4. Oathkeeper (reverse proxy)

**Applies to**: Token issuance endpoint only (`POST /wopi/token`)

**JWT claims injected by Oathkeeper**:
- `alkemio_actor_id` — the actor's UUID (from Kratos identity
  metadata_public)
- `sub` — Kratos identity ID
- `session` — full session object

**JWKS validation**: Public keys at Oathkeeper's JWKS endpoint.

**WOPI protocol endpoints** (called by Collabora) do NOT go through
Oathkeeper — they use opaque access tokens.
