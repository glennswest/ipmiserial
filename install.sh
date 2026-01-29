#!/bin/bash
set -e

TARGET_HOST="console.g11.lo"
TARGET_USER="root"
BINARY_NAME="console-server"

echo "=== Console Server Install ==="

# Build the binary
echo "Building binary..."
GOOS=linux GOARCH=arm64 go build -o ${BINARY_NAME} .

# Copy files to target
echo "Copying files to ${TARGET_HOST}..."
scp ${BINARY_NAME} ${TARGET_USER}@${TARGET_HOST}:/usr/local/bin/
scp config.yaml.example ${TARGET_USER}@${TARGET_HOST}:/tmp/config.yaml.example

# Setup on target
echo "Setting up on ${TARGET_HOST}..."
ssh ${TARGET_USER}@${TARGET_HOST} << 'ENDSSH'
set -e

# Create directories
mkdir -p /etc/console-server
mkdir -p /data/logs

# Install config if not exists
if [ ! -f /etc/console-server/config.yaml ]; then
    cp /tmp/config.yaml.example /etc/console-server/config.yaml
    echo "Config installed at /etc/console-server/config.yaml"
fi

# Make binary executable
chmod +x /usr/local/bin/console-server

# Install ipmitool if not present
if ! command -v ipmitool > /dev/null 2>&1; then
    echo "Installing ipmitool..."
    apk add --no-cache ipmitool
fi

# Stop existing instance if running
pkill -f console-server || true
sleep 1

# Start the server
cd /etc/console-server
nohup /usr/local/bin/console-server > /var/log/console-server.log 2>&1 &
sleep 2

echo ""
echo "=== Installation Complete ==="
ps aux | grep console-server | grep -v grep || echo "Process not found"
echo ""
echo "Logs: /var/log/console-server.log"
echo "Web UI: http://console.g11.lo:8080"
ENDSSH

# Cleanup local binary
rm -f ${BINARY_NAME}

echo "Done!"
