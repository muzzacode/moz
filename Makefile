SHELL := /bin/bash
BINARY := moz
INSTALL_DIR := $(HOME)/.local/bin

.PHONY: build install uninstall run test race clean clean-bin bootstrap vet help ci

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

# The TUI runs Bubble Tea commands on their own goroutines, so races here are
# real bugs rather than test artifacts. Keep this in CI.
race:
	go test -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin/

clean-bin:
	rm -rf bin/
	go clean

bootstrap:
	./bootstrap.sh

ci: vet race

help:
	@echo "Available targets:"
	@echo "  build"
	@echo "  install"
	@echo "  uninstall"
	@echo "  run"
	@echo "  test"
	@echo "  race"
	@echo "  ci"
	@echo "  vet"
	@echo "  clean"
	@echo "  clean-bin"
	@echo "  bootstrap"
	@echo "  help"