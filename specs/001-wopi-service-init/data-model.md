# Data Model: Initial WOPI Service Implementation

**Date**: 2026-03-30
**Feature**: 001-wopi-service-init

## Overview

The WOPI service stores minimal local state. File metadata and content
are owned by Alkemio (fetched via RabbitMQ). Only access tokens, locks,
and sessions are persisted locally in PostgreSQL.

## Entities

### access_tokens

Opaque DB-backed tokens for WOPI request authentication.

| Column      | Type                     | Constraints          | Notes                              |
|-------------|--------------------------|----------------------|------------------------------------|
| id          | UUID                     | PK, DEFAULT gen_random_uuid() | Internal ID               |
| token       | VARCHAR(128)             | UNIQUE, NOT NULL     | URL-safe Base64 value              |
| file_id     | VARCHAR(255)             | NOT NULL, INDEX      | Alkemio document external ID       |
| actor_id    | VARCHAR(255)             | NOT NULL             | Alkemio actor ID                   |
| permissions | VARCHAR(50)              | NOT NULL             | e.g. "read", "read,write"         |
| expires_at  | TIMESTAMPTZ              | NOT NULL, INDEX      | Token expiry (default: +8 hours)   |
| created_at  | TIMESTAMPTZ              | NOT NULL, DEFAULT NOW() |                                 |

**Lifecycle**: Created when user initiates editing session → validated
on each WOPI request → expires after 8 hours or deleted on session
end → expired tokens cleaned up by periodic job.

### locks

WOPI file locks for concurrent editing coordination.

| Column      | Type                     | Constraints          | Notes                              |
|-------------|--------------------------|----------------------|------------------------------------|
| id          | UUID                     | PK, DEFAULT gen_random_uuid() | Internal ID               |
| file_id     | VARCHAR(255)             | UNIQUE, NOT NULL     | One lock per file                  |
| lock_id     | VARCHAR(1024)            | NOT NULL             | WOPI lock ID from Collabora        |
| expires_at  | TIMESTAMPTZ              | NOT NULL, INDEX      | Lock expiry (default: +30 min)     |
| created_at  | TIMESTAMPTZ              | NOT NULL, DEFAULT NOW() |                                 |

**Lifecycle**: Created on Lock request → extended on RefreshLock →
removed on Unlock → auto-expires after 30 minutes if not refreshed.

**Constraint**: `file_id` is UNIQUE — only one lock per file at a time.

### wopi_sessions

Tracks active editing sessions for auditing and cleanup.

| Column      | Type                     | Constraints          | Notes                              |
|-------------|--------------------------|----------------------|------------------------------------|
| id          | UUID                     | PK, DEFAULT gen_random_uuid() | Internal ID               |
| file_id     | VARCHAR(255)             | NOT NULL, INDEX      | Alkemio document external ID       |
| actor_id    | VARCHAR(255)             | NOT NULL             | Alkemio actor ID                   |
| token_id    | UUID                     | NOT NULL, FK → access_tokens(id) | Associated token    |
| created_at  | TIMESTAMPTZ              | NOT NULL, DEFAULT NOW() |                                 |

**Lifecycle**: Created alongside access token → used for session
tracking and cleanup → removed when token expires or is revoked.

## Relationships

```
access_tokens 1 ←→ 1 wopi_sessions (via token_id FK)
locks: standalone (no FK to other tables — file_id is Alkemio's ID)
```

## Indexes

- `access_tokens.token` — UNIQUE index for fast lookup on every WOPI
  request
- `access_tokens.file_id` — for finding tokens by file
- `access_tokens.expires_at` — for cleanup job queries
- `locks.file_id` — UNIQUE index for lock-per-file enforcement
- `locks.expires_at` — for expired lock cleanup
- `wopi_sessions.file_id` — for listing active sessions per file

## Not Stored Locally

The following data is NOT persisted in the WOPI service database:

- **File metadata** (name, size, owner, modified time) — looked up
  from Alkemio PostgreSQL database (read-only)
- **File content** — read/written via file-service private
  endpoints
- **Actor identity** — extracted from Oathkeeper JWT on token
  issuance
- **Authorization decisions** — checked via NATS
  auth-evaluation-service

## Migration Strategy

- Use golang-migrate with `embed.FS`
- Migrations run at startup from `cmd/server/main.go`
- Naming: `000001_create_access_tokens.up.sql` /
  `000001_create_access_tokens.down.sql`
