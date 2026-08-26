GOBIN ?= $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: build fmt vet test test/nvim test/all check install

# Compile every package without installing.
build:
	go build ./...

# Fail when any file is not gofmt-formatted.
fmt:
	@files="$$(gofmt -l cmd internal)"; \
	if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi

vet:
	go vet ./...

# The Go test suite. The full run takes ~10 minutes; internal/cli is the
# slow package (it drives real git and tmux). Target one package or -run a
# pattern while iterating.
test:
	go test ./...

# The Neovim plugin suite. Needs nvim and tmux on PATH.
test/nvim:
	bash nvim/twt.nvim/tests/test.sh

# Every test suite.
test/all: test test/nvim

# The full local gate: format, vet, and the Go tests.
check: fmt vet test

# Install the twt binary to GOBIN.
install:
	mkdir -p "$(GOBIN)"
	GOBIN="$(GOBIN)" go install ./cmd/twt
