# wopi-service

A Go microservice that implements the [WOPI protocol](https://learn.microsoft.com/en-us/microsoft-365/cloud-storage-partner-program/online/) so that **Collabora Online** can open, view, and edit Alkemio documents directly in the browser.

It sits between three systems:

- **Collabora Online** — the editor that talks WOPI to this service.
- **file-service** — where document bytes and metadata actually live.
- **authorization-evaluation-service** — decides whether an actor may read/write a document.

The Alkemio frontend asks this service for an editor URL + access token for a
document; the service authorizes the actor, hands back a ready-to-embed
Collabora editor URL, and from then on Collabora calls back here to fetch
metadata, stream content, save, and lock the file.

## What it does (request flow)

```
                 ┌─────────────────────────────────────────────────────────┐
                 │                       wopi-service                        │
 Frontend ──POST/wopi/token──▶  authorize actor (read / update-content)     │
                 │              resolve Collabora editor URL (discovery)     │
                 │              mint opaque access token (stored in PG)      │
                 ◀── editorURL + access_token + WOPISrc ──────────────────── │
                 │                                                           │
 Collabora ─GET /wopi/files/{id} ─────────▶ CheckFileInfo  (metadata)       │
           ─GET /wopi/files/{id}/contents ▶ GetFile        (stream bytes)   │──▶ file-service
           ─POST/wopi/files/{id}/contents ▶ PutFile        (save bytes)     │
           ─POST/wopi/files/{id} ─────────▶ Lock/Unlock/Refresh/Relock      │──▶ PostgreSQL (locks)
                 └─────────────────────────────────────────────────────────┘
```

1. **Token issuance** (`POST /wopi/token`) — the only actor-facing endpoint.
   Identity arrives as the `X-Alkemio-Actor-Id` header (set by Traefik's
   `alkemio-resolve` forwardAuth). The service looks up the document in
   file-service, checks `read` (required) and `update-content` (optional →
   determines read-only vs. read/write) against the authorization service,
   then returns an opaque `access_token`, the `WOPISrc`, and a fully-built
   **Collabora editor URL** (derived from WOPI discovery for the document's
   MIME type). The token is persisted in the WOPI database.

2. **WOPI protocol endpoints** — called by Collabora using the opaque token
   (`?access_token=…`). Each request is gated by **token validation** and —
   when `WOPI_PROOF_VALIDATION` is enabled (the default) — **WOPI proof-key
   signature validation** (RSA-SHA256, keys from discovery).
   - `CheckFileInfo` → file metadata + permissions.
   - `GetFile` / `PutFile` → stream content to/from file-service.
   - `Lock` / `Unlock` / `RefreshLock` / `UnlockAndRelock` → collaborative
     edit locks, stored in PostgreSQL.

## Key behaviours worth knowing

- **Discovery cache** — Collabora's `/hosting/discovery` XML (editor actions
  per MIME type + proof keys) is fetched lazily and cached 12h. Proof-key
  validation failure triggers an invalidate-and-refresh (handles key rotation).
- **Editor URL building** — the discovery `urlsrc` has its Collabora-internal
  host swapped for `WOPI_BASE_URL`, WOPI template placeholders (`<ui=…&>`)
  stripped, and `WOPISrc` / `access_token` / `access_token_ttl` appended.
- **Zombie-lock takeover** — a lock held by a *different* lockID past
  `WOPI_MAX_LOCK_LIFETIME` (default 4h) can be atomically taken over by a new
  session, defending against dead Collabora DocBrokers that refresh forever.
  Same-lockID refreshes are never capped. Set to `0` to disable (legacy
  unbounded behaviour).
- **Background cleanup** — a loop every 15 min deletes expired tokens and locks.
- **Two-URL split** — `WOPI_BASE_URL` is the browser-facing editor origin;
  `WOPI_CALLBACK_URL` is the cluster-internal URL Collabora uses for the
  `WOPISrc` callback (defaults to `WOPI_BASE_URL`).

## Architecture

Hexagonal (ports & adapters). The domain core has **zero infrastructure
imports**; everything external is reached through a port interface with a
concrete adapter.

```
cmd/server/                 wiring + startup (migrations, DI, HTTP server)
internal/
  domain/
    model/                  AccessToken, Lock, FileInfo, MIME mapping (pure)
    port/                   interfaces: FileService, AuthService,
                            TokenRepository, LockRepository, DiscoveryClient
    service/                use cases: TokenService, WOPIService,
                            DiscoveryService, CleanupService
  adapter/
    inbound/http/           chi router, handlers, middleware
                            (actor header, token auth, proof validation, logging)
    outbound/
      postgres/             token + lock repositories (sqlc-generated queries)
      fileservice/          file-service h2c client (metadata + content)
      authhttp/             authorization-evaluation-service over h2c (preferred)
      nats/                 authorization over NATS (legacy fallback)
      authbreaker/          circuit breaker wrapping whichever auth transport
      collabora/            discovery XML client
migrations/                 golang-migrate SQL (embedded, run on startup)
```

**Auth transport** is chosen at startup: h2c (`AUTH_SERVICE_URL`) is preferred,
NATS (`NATS_URL`) is the fallback — at least one must be set. Either way the
call is wrapped in a circuit breaker.

## Tech stack

Go 1.26 · chi v5 (router) · pgx v5 + sqlc (PostgreSQL) · golang-migrate ·
Zap (logging) · HTTP/2 cleartext (h2c) for cluster-internal calls.

## Running

There are two ways to run the service locally.

### 1. Locally, via an env file

Run the Go binary directly on your machine, with configuration loaded from a
local env file. You provide your own PostgreSQL (and the auth / file / Collabora
services it talks to).

```bash
cp .env.example .env.local   # then edit .env.local for your setup
make run                     # builds, sources .env.local, runs the binary
```

`make run` builds the binary and sources `.env.local` before starting it (it
falls back to the ambient process environment when the file is absent). Point it
at a different file with `make run ENV_FILE=.env.dev`. At minimum
`WOPI_TOKEN_SECRET` and an auth transport (`AUTH_SERVICE_URL` or `NATS_URL`) must
be set — see [Configuration](#configuration) and the comments in
`.env.example`.

### 2. Building a Docker image locally

Build the image from the multi-stage `Dockerfile`, or bring up the bundled
`docker compose` stack that builds the image and also runs a PostgreSQL instance.

```bash
make docker-build   # build a standalone image: docker build -t alkemio/wopi-service .

make docker-up      # build + start service + PostgreSQL via docker-compose.yml
make docker-down    # tear the stack down
```

These are independent: `make docker-up` runs `docker compose up -d`, and because
`docker-compose.yml` declares `build: .`, compose builds its own image — it does
*not* reuse the `alkemio/wopi-service` tag from `make docker-build`. The service's
environment is set inline in `docker-compose.yml` (adjust it for your auth / file
/ Collabora endpoints).

### Either way

The service runs migrations against its own database on startup, primes the
discovery cache, then listens on `WOPI_SERVER_PORT` (default `8080`).

Other useful targets:

```bash
make build          # compile only
make test           # run tests
make openapi        # regenerate openapi.yaml (pre-commit hook enforces freshness)
make install-hooks  # install the pre-commit hook (once per clone)
golangci-lint run   # lint before committing
```

## Configuration

All configuration is via environment variables. The essentials:

| Variable | Purpose | Default |
|---|---|---|
| `WOPI_DATABASE_HOST/PORT/USERNAME/PASSWORD/NAME` | WOPI service's own PostgreSQL (tokens, locks) | `localhost`/`5432`/`postgres`/`postgres`/`wopi` |
| `AUTH_SERVICE_URL` | authorization-evaluation-service over h2c (**preferred**) | — |
| `NATS_URL` | authorization over NATS (fallback if `AUTH_SERVICE_URL` unset) | — |
| `FILE_SERVICE_URL` | file-service base URL | `http://localhost:4003` |
| `WOPI_COLLABORA_URL` | Collabora Online base URL (for discovery) | `http://localhost:9980` |
| `WOPI_BASE_URL` | browser-facing editor origin | `http://localhost:8080` |
| `WOPI_CALLBACK_URL` | cluster-internal `WOPISrc` URL for Collabora | = `WOPI_BASE_URL` |
| `WOPI_FRONTEND_ORIGIN` | embedding page origin → WOPI `PostMessageOrigin` | origin of `WOPI_BASE_URL` |
| `WOPI_TOKEN_SECRET` | token signing secret (**required**) | — |
| `WOPI_PROOF_VALIDATION` | enforce Collabora proof-key signatures | `true` |
| `WOPI_MAX_LOCK_LIFETIME` | zombie-lock takeover threshold (`0` disables) | `4h` |
| `WOPI_SERVER_PORT` | listen port | `8080` |
| `AUTH_BREAKER_FAILURE_THRESHOLD` / `_TIMEOUT_SECONDS` / `_HALF_OPEN_MAX_REQUESTS` | auth circuit breaker tuning | `3` / `15` / `2` |

See `internal/config/config.go` for the full list and validation rules, and
`CLAUDE.md` for architecture rules and integration context.
