# Multi-stage build for console-server
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o console-server .

# Runtime: minimal scratch image
FROM scratch

COPY --from=builder /app/console-server /console-server
COPY config.yaml.example /config.yaml

EXPOSE 80

ENTRYPOINT ["/console-server", "-config", "/config.yaml"]
