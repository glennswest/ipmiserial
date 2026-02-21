FROM golang:1.24 AS builder

ARG VERSION=dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=${VERSION}" -o ipmiserial .

FROM scratch
COPY --from=builder /build/ipmiserial /ipmiserial
COPY --from=builder /build/config.yaml.example /config.yaml
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 80
ENTRYPOINT ["/ipmiserial", "-config", "/config.yaml"]
