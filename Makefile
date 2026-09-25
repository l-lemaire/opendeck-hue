# Makefile for the streamdeck project.
#
# Common targets:
#   make build      compile ./cmd/hue into ./bin/hue for this machine
#   make run        build and run the CLI, e.g. make run ARGS="lights list"
#   make test       run all unit tests
#   make check      gofmt + go vet + tests (run before committing)
#   make cross      build hue for every OpenDeck target triple into ./dist/
#   make clean      remove build outputs

# ---------- settings ----------

# The binary name and its package path inside the module.
BIN      := hue
PKG      := ./cmd/hue

# Version string baked into the binary. Uses the git tag when available,
# otherwise "dev". The `-X` linker flag overwrites the `version` variable
# declared in cmd/hue/main.go.
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

# CGO_ENABLED=0 produces a fully static binary with no C dependencies.
# That is what lets the plugin run on a machine with nothing installed.
export CGO_ENABLED := 0

# OpenDeck identifies binaries by Rust-style target triples. Map each one to
# the GOOS/GOARCH pair Go uses. Format: <triple>:<GOOS>:<GOARCH>
TARGETS := \
	x86_64-unknown-linux-gnu:linux:amd64 \
	aarch64-unknown-linux-gnu:linux:arm64 \
	x86_64-apple-darwin:darwin:amd64 \
	aarch64-apple-darwin:darwin:arm64 \
	x86_64-pc-windows-msvc:windows:amd64

# ---------- everyday targets ----------

.PHONY: build run test vet fmt fmt-check check cross clean tidy

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) $(PKG)

# Pass arguments with ARGS, e.g.: make run ARGS="--debug lights list"
run: build
	./bin/$(BIN) $(ARGS)

test:
	go test ./...

# go vet reports likely mistakes the compiler accepts (bad printf verbs,
# unreachable code, copied locks ...).
vet:
	go vet ./...

# gofmt is the one true formatter; there is no style debate in Go.
fmt:
	gofmt -l -w .

# Fails if any file is not gofmt-clean. Useful in CI.
fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "files need gofmt:"; gofmt -l .; exit 1; }

check: fmt-check vet test

# go mod tidy adds missing and removes unused dependencies in go.mod/go.sum.
tidy:
	go mod tidy

# ---------- OpenDeck plugin ----------

PLUGIN_ID   := com.github.llemaire.hue.sdPlugin
PLUGIN_DIR  := dist/$(PLUGIN_ID)
PLUGIN_BIN  := opendeck-hue
PLUGIN_PKG  := ./cmd/opendeck-hue
OPENDECK_PLUGINS := $(HOME)/.config/opendeck/plugins

.PHONY: plugin plugin-install plugin-uninstall opendeck-restart plugin-log

# Assemble the plugin folder for this machine (Linux x86_64) under dist/:
# the manifest, icons and property inspector from plugin/, plus the binary
# at the path the manifest's CodePaths expects.
plugin:
	rm -rf $(PLUGIN_DIR)
	mkdir -p $(PLUGIN_DIR)
	cp -r plugin/. $(PLUGIN_DIR)/
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(PLUGIN_DIR)/x86_64-unknown-linux-gnu/bin/$(PLUGIN_BIN) $(PLUGIN_PKG)

# Copy the assembled folder into OpenDeck's plugin directory. A symlink
# would be nicer for development, but OpenDeck skips symlinked entries when
# it scans the directory. OpenDeck loads plugins at startup, so restart it
# afterwards (make opendeck-restart).
plugin-install: plugin
	mkdir -p $(OPENDECK_PLUGINS)
	rm -rf $(OPENDECK_PLUGINS)/$(PLUGIN_ID)
	cp -r $(PLUGIN_DIR) $(OPENDECK_PLUGINS)/$(PLUGIN_ID)
	@echo "installed: $(OPENDECK_PLUGINS)/$(PLUGIN_ID)"

plugin-uninstall:
	rm -rf $(OPENDECK_PLUGINS)/$(PLUGIN_ID)

# Restart the OpenDeck desktop app so it picks up the plugin. setsid detaches
# it from this terminal; the sleep gives the old process time to exit.
opendeck-restart:
	-pkill -x opendeck
	sleep 1
	setsid -f opendeck >/dev/null 2>&1
	@echo "OpenDeck restarted; see: make plugin-log"

# Follow both logs: OpenDeck's own and the plugin's.
plugin-log:
	tail -n 20 -f $(HOME)/.local/share/opendeck/logs/opendeck.log $${XDG_STATE_HOME:-$(HOME)/.local/state}/opendeck-hue/plugin.log

# ---------- cross-compilation ----------

# Builds one binary per target into dist/<triple>/bin/<name>[.exe], matching
# the directory layout OpenDeck expects inside a .sdPlugin folder.
cross:
	@for t in $(TARGETS); do \
		triple=$${t%%:*}; rest=$${t#*:}; goos=$${rest%%:*}; goarch=$${rest#*:}; \
		ext=""; [ "$$goos" = "windows" ] && ext=".exe"; \
		out=dist/$$triple/bin/$(BIN)$$ext; \
		echo "  $$goos/$$goarch -> $$out"; \
		GOOS=$$goos GOARCH=$$goarch go build -trimpath -ldflags '$(LDFLAGS)' -o $$out $(PKG) || exit 1; \
	done

clean:
	rm -rf bin dist coverage.out
