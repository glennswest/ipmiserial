#!/bin/bash
set -e

ROUTER="192.168.1.88"
CONTAINER="console.g11.lo"
BINARY_NAME="console-server"
REMOTE_PATH="/raid1/images/${CONTAINER}/usr/local/bin/${BINARY_NAME}"

echo "=== Console Server Update ==="

# Build static binary (no CGO, no libc needed)
echo "Building static binary for arm64..."
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o ${BINARY_NAME} .

# Stop container
echo "Stopping container..."
ssh admin@${ROUTER} "/container/stop [find name=\"${CONTAINER}\"]" 2>/dev/null || true
sleep 2

# Copy binary to container filesystem
echo "Copying binary..."
scp ${BINARY_NAME} admin@${ROUTER}:${REMOTE_PATH}

# Start container
echo "Starting container..."
ssh admin@${ROUTER} "/container/start [find name=\"${CONTAINER}\"]"
sleep 3

# Check status
echo "Checking status..."
ssh admin@${ROUTER} "/container print where name=\"${CONTAINER}\""

# Cleanup local binary
rm -f ${BINARY_NAME}

echo ""
echo "Update complete!"
echo "Console server: http://console.g11.lo"
