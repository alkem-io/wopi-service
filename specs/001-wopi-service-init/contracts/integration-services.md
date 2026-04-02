# Integration Service Contracts

**Date**: 2026-04-02 (updated)
**Feature**: 001-wopi-service-init

## Overview

The WOPI service integrates with four external services. Auth transport
is configurable (h2c preferred, NATS fallback).

---

## 1. Authorization Evaluation Service

Two transports are supported, selected by env vars. h2c is preferred
when `AUTH_SERVICE_URL` is set; NATS is the fallback when only
`NATS_URL` is set. At least one must be configured.

Both transports are wrapped with a shared circuit breaker (gobreaker):
- `AUTH_BREAKER_FAILURE_THRESHOLD` (default: 3)
- `AUTH_BREAKER_TIMEOUT_SECONDS` (default: 15)
- `AUTH_BREAKER_HALF_OPEN_MAX_REQUESTS` (default: 2)

### h2c Transport (preferred)

**URL**: configurable via `AUTH_SERVICE_URL`
**Protocol**: HTTP/2 cleartext (h2c) with connection multiplexing
**Retry**: 3 attempts with exponential backoff (50ms, 100ms, 200ms)
on transient errors (connection reset, 503)

```
POST /internal/auth/evaluate
Content-Type: application/json
```

**Request**:
```json
{
  "agentId": "<alkemio-actor-uuid>",
  "privilege": "read",
  "authorizationPolicyId": "<document-auth-policy-uuid>"
}
```

**Response (200 OK)**:
```json
{
  "allowed": true,
  "reason": "Granted privilege 'read' using credential rule '...'"
}
```

**Response (400 Bad Request)**: validation error (invalid UUID, etc.)
**Response (503 Service Unavailable)**: triggers retry

### NATS Transport (fallback)

**Subject**: `auth.evaluate`
**Protocol**: NATS request-reply

**Request** (NATS-wrapped):
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

**Response**: same JSON as h2c (`{allowed, reason}`).

### Privileges used by WOPI service
- `read` — required to view/download document
- `update-content` — required to edit document

**Used by**: Token issuance endpoint (determine read/write
permissions before issuing WOPI access token).

---

## 2. File Service Go (HTTP, cluster-internal)

**Base URL**: configurable via `FILE_SERVICE_URL`
(default: `http://localhost:4003`)
**Timeout**: 30 seconds

All access is by document ID. ExternalID is never exposed to the
WOPI service.

### Get Document Metadata

```
GET /internal/document/:id/meta
```

**Response** `200 OK`:
```json
{
  "id": "a1b2c3d4-...",
  "externalID": "sha3-256-hash",
  "mimeType": "application/vnd.openxmlformats-...",
  "size": 45678,
  "displayName": "report.docx",
  "authorizationId": "d4e5f6a7-..."
}
```

Note: The JSON field is `authorizationId` (Alkemio DB column name,
TypeORM convention). The WOPI service maps this to
`AuthorizationPolicyID` in its domain model. Both refer to the
same UUID — the FK to the `authorization_policy` table.

**Used by**: Token issuance (get authorization policy ID for NATS/h2c
auth check), CheckFileInfo (file metadata).

### Read File Content

```
GET /internal/document/:id/content
```

**Response** `200 OK`: Raw binary file content with Content-Type.
**Status codes**: 200, 404

**Used by**: GetFile (stream content to Collabora).

### Write File Content (store-and-link)

```
PUT /internal/document/:id/content
Content-Type: application/octet-stream
```

**Request body**: Raw binary file content.
**Response** `200 OK`:
```json
{
  "externalID": "sha3-256-hash",
  "mimeType": "image/jpeg",
  "size": 45678
}
```

**Status codes**: 200, 404, 500

**Used by**: PutFile (save edited content from Collabora).

### Check File Exists

```
HEAD /internal/document/:id/content
```

**Status codes**: 200 (exists), 404 (not found)

---

## 3. Oathkeeper (reverse proxy)

**Applies to**: Token issuance endpoint only (`POST /wopi/token`)

**JWT claims injected by Oathkeeper**:
- `alkemio_actor_id` — the actor's UUID (from Kratos identity
  metadata_public)

**WOPI protocol endpoints** (called by Collabora) do NOT go through
Oathkeeper — they use opaque access tokens.

---

## 4. Collabora Online

**Discovery URL**: `{WOPI_COLLABORA_URL}/hosting/discovery`
**Timeout**: 30 seconds
**Cache**: 12 hours, stale fallback on refresh failure

Provides:
- Supported file types and editor action URLs
- RSA proof keys for WOPI request signature validation
