# Relay — common tasks. `make check` must pass before every commit.
SAFE := scripts/dev/safe
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X github.com/aduthekaddu/relay/internal/version.Version=$(VERSION) -X github.com/aduthekaddu/relay/internal/version.Commit=$(COMMIT)

.PHONY: all build web go test test-go test-web check fmt vet lint dev-go web-dev site site-dev clean release

all: build

build: web go

web:
	cd web && ../$(SAFE) pnpm build

go:
	$(SAFE) go build -trimpath -ldflags '$(LDFLAGS)' -o bin/relay ./cmd/relay

test: test-go test-web

test-go:
	$(SAFE) go test ./...

test-web:
	cd web && ../$(SAFE) pnpm test

fmt:
	gofmt -w cmd internal
	cd web && pnpm format

vet:
	$(SAFE) go vet ./...

lint:
	test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	cd web && pnpm typecheck && pnpm lint

check: lint vet test

dev-go:
	scripts/dev/run.sh

web-dev:
	cd web && RELAY_DEV_URL=http://127.0.0.1:47700 pnpm dev

site:
	cd site && ../$(SAFE) pnpm build

site-dev:
	cd site && pnpm dev --port 47790

clean:
	rm -rf bin internal/web/dist/assets

# Cross-compile release binaries (web must be built first).
release: web
	for p in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(SAFE) go build -trimpath -ldflags '$(LDFLAGS)' -o dist/relay_$${os}_$${arch} ./cmd/relay; \
	done
	cd dist && sha256sum relay_* > checksums.txt
