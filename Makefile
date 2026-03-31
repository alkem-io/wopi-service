.PHONY: build run test lint generate openapi docker-build docker-up docker-down clean

# Binary
BINARY := wopi-service
CMD := ./cmd/server

# Build
build:
	go build -o $(BINARY) $(CMD)

run: build
	./$(BINARY)

# Code generation
generate:
	sqlc generate

openapi:
	apispec --dir . --output openapi.yaml --config apispec.yaml --skip-cgo

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
