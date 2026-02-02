# Scratch container - just the static Go binary
# Build with: CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o console-server .
# Then create minimal container image

FROM scratch

# Copy the pre-built static binary
COPY console-server /console-server

# Copy config to working directory
COPY config.yaml.example /config.yaml

EXPOSE 80

ENTRYPOINT ["/console-server", "-config", "/config.yaml"]
