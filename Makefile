.PHONY: build deploy run clean

BINARY = ipmiserial

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

deploy:
	./deploy.sh

clean:
	rm -f $(BINARY)
