BUN ?= bun
GOBIN ?= $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif
HAS_BUN := $(shell command -v $(BUN) 2>/dev/null)

.PHONY: build-cursor-harness test-cursor-harness test install install-twt install-cursor-harness

build-cursor-harness:
	cd cursor-harness && $(BUN) install --frozen-lockfile
	cd cursor-harness && $(BUN) build --compile src/index.ts --outfile dist/twt-cursor-cloud

test-cursor-harness:
	cd cursor-harness && $(BUN) install --frozen-lockfile
	cd cursor-harness && $(BUN) test
	cd cursor-harness && $(BUN) run typecheck

test: test-cursor-harness
	go test ./...

# install puts twt on GOBIN, and the Cursor Cloud harness with it when bun
# is available. Without bun the harness is skipped: local dispatch and every
# ticket command work; only cursor-cloud dispatch needs the harness.
install: install-twt
ifdef HAS_BUN
	$(MAKE) install-cursor-harness
else
	@echo "bun is not installed: skipped the twt-cursor-cloud harness."
	@echo "Local dispatch works without it. For Cursor Cloud dispatch,"
	@echo "install bun (https://bun.sh) and run 'make install-cursor-harness'."
endif

install-twt:
	mkdir -p "$(GOBIN)"
	GOBIN="$(GOBIN)" go install ./cmd/twt

install-cursor-harness: build-cursor-harness
	install -m 0755 cursor-harness/dist/twt-cursor-cloud "$(GOBIN)/twt-cursor-cloud"
