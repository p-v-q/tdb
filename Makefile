GO ?= go
BIN ?= bin/tdb
PKGS ?= ./...

.PHONY: all build test race vet fmt fmt-check check demo clean

all: check build

build:
	$(GO) build -trimpath -o $(BIN) ./cmd/tdb

test:
	$(GO) test -count=1 $(PKGS)

race:
	$(GO) test -count=1 -race ./ui ./history ./complete ./driver/... ./settings ./sql ./textbuf

vet:
	$(GO) vet $(PKGS)

fmt:
	gofmt -w .

fmt-check:
	@files="$$(gofmt -l .)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt needed:"; echo "$$files"; exit 1; \
	fi

check: fmt-check vet test

demo:
	cd .itest && $(GO) run ./cmd/demo -root .. -out ../.artifacts/demo

clean:
	rm -rf bin
