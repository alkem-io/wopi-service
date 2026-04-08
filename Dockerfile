# Build Stage
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETARCH

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git wget ca-certificates tzdata

# Download the wait script (architecture-specific)
RUN if [ "$TARGETARCH" = "arm64" ]; then \
      wget -O /wait https://github.com/ufoscout/docker-compose-wait/releases/download/2.12.1/wait_aarch64; \
    elif [ "$TARGETARCH" = "amd64" ]; then \
      wget -O /wait https://github.com/ufoscout/docker-compose-wait/releases/download/2.12.1/wait; \
    else \
      echo "Unsupported architecture: $TARGETARCH" && exit 1; \
    fi && chmod +x /wait

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application for target architecture
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -o wopi-service ./cmd/server

# Final Stage - Distroless
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
# Copy CA certificates and timezone data from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
# Copy wait script from builder
COPY --from=builder /wait /wait
# Copy binary from builder
COPY --from=builder /app/wopi-service .
# Expose WOPI service port
EXPOSE 8080
ENTRYPOINT ["/app/wopi-service"]
