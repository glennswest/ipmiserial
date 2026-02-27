#!/bin/bash
# Build ipmiserial binary and container image locally
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

REGISTRY="registry.gt.lo:5000"
IMAGE="$REGISTRY/ipmiserial:edge"

VERSION=$(cat VERSION 2>/dev/null | tr -d '\n' || echo "0.0.0")
GIT_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
FULL_VERSION="${VERSION}+${GIT_HASH}"

echo "=== Building ipmiserial ${FULL_VERSION} ==="

# Cross-compile binary locally for ARM64 Linux
echo "Building binary for arm64..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=vendor \
  -ldflags="-s -w -X main.Version=${FULL_VERSION}" \
  -o ipmiserial .

# Build scratch container image with podman
echo "Building container image..."
podman build --platform linux/arm64 -t "$IMAGE" .

# Clean up local binary
rm -f ipmiserial

echo ""
echo "=== Build complete ==="
echo "Image: $IMAGE"
echo "Run ./deploy.sh to push and deploy to rose1"
