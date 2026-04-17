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

echo "Building Docker image..."
docker compose down
docker compose build --no-cache
docker compose up -d
echo "Done. Showing logs (Ctrl+C to exit)..."
docker compose logs -f --tail 30
