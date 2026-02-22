FROM golang:1.24 AS builder

ARG VERSION=dev

WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-s -w -X main.Version=${VERSION}" -o ipmiserial .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/ipmiserial /ipmiserial
COPY --from=builder /build/config.yaml.example /config.yaml
EXPOSE 80
ENTRYPOINT ["/ipmiserial", "-config", "/config.yaml"]
