BUN ?= bun
GOBIN ?= $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build-cursor-harness test-cursor-harness test install

build-cursor-harness:
	cd cursor-harness && $(BUN) install --frozen-lockfile
	cd cursor-harness && $(BUN) build --compile src/index.ts --outfile dist/twt-cursor-cloud

test-cursor-harness:
	cd cursor-harness && $(BUN) install --frozen-lockfile
	cd cursor-harness && $(BUN) test
	cd cursor-harness && $(BUN) run typecheck

test: test-cursor-harness
	go test ./...

install: build-cursor-harness
	mkdir -p "$(GOBIN)"
	GOBIN="$(GOBIN)" go install ./cmd/twt
	install -m 0755 cursor-harness/dist/twt-cursor-cloud "$(GOBIN)/twt-cursor-cloud"
