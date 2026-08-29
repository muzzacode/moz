SHELL := /bin/bash
BINARY := moz
INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall run test clean bootstrap

build:
	go build -o bin/$(BINARY) ./cmd/moz

install: build
	install -d $(INSTALL_DIR)
	install -m 0755 bin/$(BINARY) $(INSTALL_DIR)/$(BINARY)

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

run: build
	./bin/$(BINARY)

test:
	go test ./...

clean:
	rm -rf bin/

bootstrap:
	./bootstrap.sh
