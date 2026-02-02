# Scratch container - just the static Go binary
# Build with: CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o console-server .
# Then create minimal container image

FROM scratch

# Copy the pre-built static binary
COPY console-server /console-server

# Copy config
COPY config.yaml.example /etc/console-server/config.yaml

EXPOSE 80

ENTRYPOINT ["/console-server"]
