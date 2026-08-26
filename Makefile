GOBIN ?= $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif

.PHONY: test install

test:
	go test ./...

install:
	mkdir -p "$(GOBIN)"
	GOBIN="$(GOBIN)" go install ./cmd/twt
