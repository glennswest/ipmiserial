#!/bin/bash
# Build, push, and deploy ipmiserial to mkube on rose1
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

REGISTRY="192.168.200.2:5000"
MKUBE_API="http://192.168.200.2:8082"
IMAGE="$REGISTRY/ipmiserial:edge"

VERSION=$(cat VERSION 2>/dev/null | tr -d '\n' || echo "0.0.0")
GIT_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
FULL_VERSION="${VERSION}+${GIT_HASH}"

echo "=== Deploying ipmiserial ${FULL_VERSION} ==="

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

# Push to local registry (mkube will push to GHCR)
echo "Pushing to $REGISTRY..."
podman push --tls-verify=false "$IMAGE"

# Trigger mkube registry poll to update the container
echo "Triggering registry update..."
curl -s -X POST "$MKUBE_API/api/v1/registry/poll"

echo ""
echo "=== Done ==="
echo "Deployed ipmiserial $FULL_VERSION"
echo "Available at http://ipmiserial.g11.lo"
