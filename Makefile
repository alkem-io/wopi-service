.PHONY: build run test lint generate openapi install-hooks docker-build docker-up docker-down clean

# Binary
BINARY := wopi-service
CMD := ./cmd/server

# Local env file sourced by `run` when present (override: make run ENV_FILE=.env.dev).
# Falls back to the ambient process environment when the file is absent — so the
# same target works for a local clone (.env.local) and in a container/CI (env injected).
ENV_FILE ?= .env.local

# Build
build:
	go build -o $(BINARY) $(CMD)

run: build
	@set -a; \
	if [ -f $(ENV_FILE) ]; then echo "→ loading env from $(ENV_FILE)"; . ./$(ENV_FILE); fi; \
	set +a; \
	exec ./$(BINARY)

# Code generation
generate:
	sqlc generate

openapi:
	apispec --dir . --output openapi.yaml --config apispec.yaml --skip-cgo

# Hooks: point git at .githooks/ so pre-commit auto-regenerates openapi.yaml
# whenever Go sources change. Run once per clone (or after deleting .git/).
install-hooks:
	git config core.hooksPath .githooks
	@echo "Installed git hooks from .githooks/ — bypass with 'git commit --no-verify'."

# Quality
test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

# Docker
docker-build:
	docker build -t alkemio/$(BINARY) .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

# Cleanup
clean:
	rm -f $(BINARY)
	go clean
