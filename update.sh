#!/bin/bash
set -e

TARGET_HOST="console.g11.lo"
TARGET_USER="root"
BINARY_NAME="console-server"

echo "=== Console Server Update ==="

# Build the binary
echo "Building binary..."
GOOS=linux GOARCH=arm64 go build -o ${BINARY_NAME} .

# Stop existing instance first
echo "Stopping service..."
ssh ${TARGET_USER}@${TARGET_HOST} "pkill -f console-server; sleep 1" || true

# Copy binary to target
echo "Copying binary to ${TARGET_HOST}..."
scp ${BINARY_NAME} ${TARGET_USER}@${TARGET_HOST}:/usr/local/bin/

# Start on target
echo "Starting service..."
ssh ${TARGET_USER}@${TARGET_HOST} << 'ENDSSH'
chmod +x /usr/local/bin/console-server
cd /etc/console-server
nohup /usr/local/bin/console-server > /var/log/console-server.log 2>&1 &
sleep 2
ps aux | grep console-server | grep -v grep || echo "Process not found"
ENDSSH

# Cleanup local binary
rm -f ${BINARY_NAME}

echo ""
echo "Update complete!"
