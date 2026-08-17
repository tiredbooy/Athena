# Athena developer commands.
# User vault/DB stay under $HOME. This Makefile only builds the app.

.DEFAULT_GOAL := help

GO      ?= go
NPM     ?= npm
TUI     := apps/tui
BINARY  := bin/athena
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
LIBDIR  ?= $(PREFIX)/lib/athena
GOCACHE_FALLBACK := /tmp/athena-go-build

.PHONY: help all build engine tui tui-deps \
	run run-legacy run-tui run-engine \
	test test-go test-tui test-gocache \
	check tui-check fmt vet tidy \
	install uninstall clean doctor

help: ## Show this help
	@printf 'Athena — make <target>\n\n'
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: tui engine ## Build the Ink TUI and the Go engine

# --- build ---

engine: $(BINARY) ## Build ./bin/athena

$(BINARY):
	@mkdir -p bin
	$(GO) build -o $(BINARY) ./cmd/athena

build: all ## Alias for all

tui-deps: ## Install Ink TUI npm dependencies
	$(NPM) install --prefix $(TUI)

tui: tui-deps ## Build the TypeScript TUI into apps/tui/dist
	$(NPM) run build --prefix $(TUI)

# --- run ---

run: all ## Build and run Athena (Ink if dist exists, else Bubble Tea)
	./$(BINARY)

run-legacy: engine ## Run the Go Bubble Tea fallback
	./$(BINARY) --legacy-tui

run-tui: all ## Run and require the Ink TUI
	./$(BINARY) --tui

run-engine: engine ## Run the stdio engine only (no UI)
	./$(BINARY) engine

# --- test / check ---

test: test-go test-tui ## Run Go and TUI tests

test-go: ## Run all Go tests
	$(GO) test ./...

test-tui: tui ## Run Ink TUI tests
	$(NPM) test --prefix $(TUI)

test-gocache: ## Go tests using /tmp/athena-go-build (read-only home cache)
	GOCACHE=$(GOCACHE_FALLBACK) $(GO) test ./...

check: vet tui-check ## Static checks (go vet + tsc --noEmit)

tui-check: tui-deps ## Typecheck the Ink TUI
	$(NPM) run check --prefix $(TUI)

fmt: ## Format Go sources
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

# --- install ---

install: all ## Install binary + TUI to ~/.local (override PREFIX=)
	install -d $(LIBDIR)/apps/tui/dist $(BINDIR)
	install -m 755 $(BINARY) $(LIBDIR)/athena
	cp -R $(TUI)/dist/. $(LIBDIR)/apps/tui/dist/
	printf '%s\n' \
		'#!/bin/sh' \
		'export ATHENA_TUI_ENTRY="$(LIBDIR)/apps/tui/dist/index.js"' \
		'exec "$(LIBDIR)/athena" "$$@"' \
		> $(BINDIR)/athena
	chmod 755 $(BINDIR)/athena
	@printf 'Installed %s (TUI %s)\n' $(BINDIR)/athena $(LIBDIR)/apps/tui/dist/index.js

uninstall: ## Remove a make install
	rm -f $(BINDIR)/athena
	rm -rf $(LIBDIR)

# --- maintenance ---

doctor: ## Print Go, Node, and repo paths
	@printf 'go:     '; $(GO) version
	@printf 'node:   '; command -v node >/dev/null && node -v || echo 'missing (needed for Ink TUI)'
	@printf 'npm:    '; command -v $(NPM) >/dev/null && $(NPM) -v || echo 'missing'
	@printf 'binary: %s\n' $(BINARY)
	@printf 'tui:    %s/dist/index.js\n' $(TUI)
	@printf 'vault/db are not in this repo; defaults are ~/Athena and ~/.local/share/athena/athena.db\n'

clean: ## Remove local build outputs (does not touch user vault/DB or data/)
	rm -rf bin $(TUI)/dist
	@printf 'Left data/ and user vault/DB alone.\n'
