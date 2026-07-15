# pskillctl build / test
#
# Unlike pbrainctl, phantom-skills does NOT need the sqlite_fts5 build tag:
# the daemon is pure-Go Postgres (pgx) and the agent's write-ahead queue uses
# plain SQLite (no FTS5 / vec0). CGO is still required for mattn/go-sqlite3 on
# the agent side, but no extra cgo CFLAGS.

PKGS := ./...

VERSION  := v0.1.0-dev
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X github.com/neverprepared/phantom-skills/internal/version.Version=$(VERSION) \
            -X github.com/neverprepared/phantom-skills/internal/version.Commit=$(COMMIT) \
            -X github.com/neverprepared/phantom-skills/internal/version.BuildDate=$(DATE)

.PHONY: build test test-race vet tidy clean fmt all sqlc

all: vet test build

# Regenerate the type-safe Postgres data-access layer from migrations + query
# files into internal/pgstore. Generated code is checked in; only needed after
# editing migrations or queries. NOT part of `all`.
sqlc:
	sqlc generate

build:
	go build -ldflags="$(LDFLAGS)" -o pskillctl ./cmd/pskillctl

test:
	go test -count=1 -timeout=90s $(PKGS)

test-race:
	go test -race -count=1 -timeout=180s $(PKGS)

vet:
	go vet $(PKGS)

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -f pskillctl
