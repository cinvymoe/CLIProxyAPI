#!/bin/bash
set -e
cd "$(dirname "$0")"

VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

echo "Building binary (CGO_ENABLED=0, static linking)..."
CGO_ENABLED=0 /usr/local/go/bin/go build \
	-ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" \
	-o cli-proxy-api ./cmd/server/

echo "Building Docker image (copying pre-compiled binary)..."
docker compose down

# Use pre-compiled binary instead of building inside Docker (avoids go mod download network issues)
cat > /tmp/Dockerfile.rebuild << 'DOCKEREOF'
FROM alpine:3.23
RUN apk add --no-cache tzdata
RUN mkdir /CLIProxyAPI
COPY cli-proxy-api /CLIProxyAPI/CLIProxyAPI
COPY config.example.yaml /CLIProxyAPI/config.example.yaml
WORKDIR /CLIProxyAPI
EXPOSE 8317
CMD ["./CLIProxyAPI"]
DOCKEREOF

docker build -t cli-proxy-api:local -f /tmp/Dockerfile.rebuild .
rm -f /tmp/Dockerfile.rebuild

echo "Starting container..."
CLI_PROXY_IMAGE=cli-proxy-api:local docker compose up -d --no-build --pull never
echo "Done. Logs:"
