SHELL := /bin/bash
BINARY := moz
INSTALL_DIR := $(HOME)/.local/bin

.PHONY: build install uninstall run test clean clean-bin bootstrap vet

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

vet:
	go vet ./...

clean:
	rm -rf bin/

clean-bin:
	rm -rf bin/
	go clean

bootstrap:
	./bootstrap.sh
