# Quickstart: Alkemio WOPI Service

## Prerequisites

- Go 1.26
- PostgreSQL (running, with a database created for WOPI service)
- Authorization-evaluation-service (running, h2c or NATS)
- file-service-go (running, reachable)
- Collabora Online (running, reachable)
- Oathkeeper (running, configured for WOPI token endpoint)

## Setup

```bash
cd wopi-service
go mod download

# Own database
export WOPI_DATABASE_HOST="localhost"
export WOPI_DATABASE_PORT="5432"
export WOPI_DATABASE_USERNAME="postgres"
export WOPI_DATABASE_PASSWORD="postgres"
export WOPI_DATABASE_NAME="wopi"
export WOPI_DATABASE_TIMEOUT="5s"

# Authorization (h2c preferred — set one)
export AUTH_SERVICE_URL="http://localhost:6060"
# Or for NATS: export NATS_URL="nats://localhost:4222"

# File service
export FILE_SERVICE_URL="http://localhost:4003"

# Service
export WOPI_COLLABORA_URL="http://localhost:9980"
export WOPI_BASE_URL="http://localhost:8080"
export WOPI_TOKEN_SECRET="your-secret-key"
export WOPI_SERVER_PORT="8080"

# Run database migrations (happen at startup)
# Start the service
go run cmd/server/main.go
```

## Verify

```bash
# Liveness check
curl http://localhost:8080/live
# Expected: {"status":"ok"}

# Readiness check (requires DB connection)
curl http://localhost:8080/health
# Expected: {"status":"ok"}

# WOPI discovery (requires Collabora running)
curl http://localhost:8080/wopi/discovery
# Expected: JSON with supported file types and editor URLs
```

## Development

```bash
# Generate sqlc code after modifying .sql files
make generate

# Generate OpenAPI spec
make openapi

# Run linter
make lint

# Run tests
make test
```

## End-to-End Test Flow

1. Authenticate with Alkemio to get a session (Oathkeeper JWT)
2. Request a WOPI access token: `POST /wopi/token` with `{documentId}`
3. Receive `{accessToken, accessTokenTTL, wopiSrc}`
4. Construct Collabora editor URL using discovery data + wopiSrc + token
5. Open the editor URL in a browser (iframe)
6. Collabora calls CheckFileInfo, GetFile, Lock
7. Edit document — Collabora auto-saves via PutFile
8. Close — Collabora calls Unlock
