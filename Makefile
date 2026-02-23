.PHONY: build deploy run clean

BINARY = ipmiserial
VERSION = $(shell cat VERSION 2>/dev/null || echo "0.0.0")

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=vendor \
		-ldflags="-s -w -X main.Version=$(VERSION)" -o $(BINARY) .

run:
	go build -o $(BINARY) . && ./$(BINARY)

deploy:
	./deploy.sh

clean:
	rm -f $(BINARY)
