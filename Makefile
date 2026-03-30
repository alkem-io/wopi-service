.PHONY: build run test lint generate migrate docker-build docker-up docker-down clean

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

# Database
migrate:
	go run $(CMD) migrate

# Quality
test:
	go test ./... -count=1

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
