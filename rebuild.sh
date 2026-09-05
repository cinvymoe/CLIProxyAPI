#!/bin/bash
set -e
cd "$(dirname "$0")"

VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

echo "Building binary (CGO_ENABLED=1, required by the .so plugin loader)..."
CGO_ENABLED=1 /usr/local/go/bin/go build \
	-ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" \
	-o cli-proxy-api ./cmd/server/

echo "Building plugin .so (opencode-responses)..."
# Plugin has its own go.mod (replace => ..), so building from the repo root via
# "go build ./opencode-responses-plugin" requires a workspace or fails with
# "main module does not contain package". Try the direct path first, then
# fallback to building inside the plugin module.
if ! CGO_ENABLED=1 /usr/local/go/bin/go build -buildmode=c-shared -o plugins/opencode-responses.so ./opencode-responses-plugin; then
  echo "Fallback: building plugin inside its own module..."
  (cd opencode-responses-plugin && CGO_ENABLED=1 /usr/local/go/bin/go build -buildmode=c-shared -o ../plugins/opencode-responses.so .)
fi

echo "Building Docker image (copying pre-compiled binary)..."
docker compose down

# Use pre-compiled binary instead of building inside Docker (avoids go mod download network issues).
# Base must be glibc-based (Debian bookworm matches the host toolchain) because the
# plugin system dlopen()s .so files; alpine/musl cannot load them.
# Build from a minimal context dir: building from the repo root uploads the whole
# tree (logs/, auths/, .git) to the daemon and takes forever.
BUILD_CTX="$(mktemp -d)"
trap 'rm -rf "$BUILD_CTX"' EXIT
cp cli-proxy-api config.example.yaml "$BUILD_CTX/"
cat > "$BUILD_CTX/Dockerfile" << 'DOCKEREOF'
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends tzdata ca-certificates && rm -rf /var/lib/apt/lists/*
RUN mkdir /CLIProxyAPI
COPY cli-proxy-api /CLIProxyAPI/CLIProxyAPI
COPY config.example.yaml /CLIProxyAPI/config.example.yaml
WORKDIR /CLIProxyAPI
EXPOSE 8317
CMD ["./CLIProxyAPI"]
DOCKEREOF

# Proxy env is forwarded as predefined build args for the apt-get step.
docker build -t cli-proxy-api:local "$BUILD_CTX"

echo "Starting container..."
CLI_PROXY_IMAGE=cli-proxy-api:local docker compose up -d --no-build --pull never
echo "Done. Logs:"
