# Quickstart: Alkemio WOPI Service

## Prerequisites

- Go 1.25
- PostgreSQL (running, with a database created for WOPI service)
- NATS server (running)
- file-service-go (running, reachable)
- Alkemio Server + PostgreSQL (running, for document metadata)
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

# Alkemio database (read-only)
export ALKEMIO_DATABASE_HOST="localhost"
export ALKEMIO_DATABASE_PORT="5432"
export ALKEMIO_DATABASE_USERNAME="readonly"
export ALKEMIO_DATABASE_PASSWORD="readonly"
export ALKEMIO_DATABASE_NAME="alkemio"

# NATS
export NATS_URL="nats://localhost:4222"

# File service
export FILE_SERVICE_URL="http://localhost:4003"

# Service
export WOPI_COLLABORA_URL="http://localhost:9980"
export WOPI_BASE_URL="http://localhost:8080"
export WOPI_TOKEN_SECRET="your-secret-key"
export WOPI_SERVER_PORT="8080"

# Run database migrations
go run cmd/server/main.go migrate

# Start the service
go run cmd/server/main.go
```

## Verify

```bash
# Health check
curl http://localhost:8080/health
# Expected: {"status":"ok"}

# WOPI discovery (requires Collabora running)
curl http://localhost:8080/wopi/discovery
# Expected: JSON with supported file types and editor URLs
```

## Development

```bash
# Generate sqlc code after modifying .sql files
sqlc generate

# Run linter
golangci-lint run

# Run tests
go test ./...
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
