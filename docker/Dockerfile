# Multi-stage build for pskillctl. Ubuntu both stages so the libc matches
# across builder + runtime (an Alpine/musl builder produces a binary whose
# glibc dynamic linker isn't found at runtime).
#
# Build:
#   docker build -t pskillctl:dev -f docker/Dockerfile .
#
# Run the daemon (mount config + data):
#   docker run --rm -p 9997:9997 \
#     -v $HOME/.config/phantom-skills-server:/config:ro \
#     -v $HOME/.local/share/phantom-skills-server:/data \
#     pskillctl:dev server serve
#
# The agent (pskillctl client mcp) is NOT meant to run from this image — it
# ships on developer hosts via `brew install neverprepared/tap/pskillctl` or
# the GitHub release tarball.

# ---- builder ---------------------------------------------------------
FROM ubuntu:24.04 AS builder

ARG GO_VERSION=1.26.2
ARG TARGETARCH

# Go toolchain + cgo prereqs. build-essential brings gcc + libc-dev, which
# mattn/go-sqlite3 (the agent write-ahead queue, linked into the single binary)
# needs. No FTS5/vec — unlike pbrainctl there is no sqlite_fts5 build tag.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl tar build-essential git \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" \
        | tar -C /usr/local -xz
ENV PATH=/usr/local/go/bin:$PATH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# CGO_ENABLED=1: mattn/go-sqlite3 is cgo. ldflags strip debug symbols and
# stamp the version so `pskillctl version` is accurate.
ENV CGO_ENABLED=1
ARG VERSION=docker
RUN go build -ldflags="-w -s \
        -X github.com/neverprepared/phantom-skills/internal/version.Version=${VERSION}" \
    -o /out/pskillctl ./cmd/pskillctl

# ---- runtime ---------------------------------------------------------
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r pskills && useradd -r -g pskills -d /data -s /sbin/nologin pskills

COPY --from=builder /out/pskillctl /usr/local/bin/pskillctl

ENV PSKILLS_CONFIG_DIR=/config \
    PSKILLS_DATA_DIR=/data

EXPOSE 9997
VOLUME ["/config", "/data"]

# Drop privileges. The /config and /data bind mounts must be readable/writable
# by uid of the pskills user.
USER pskills

ENTRYPOINT ["/usr/local/bin/pskillctl"]
CMD ["server", "serve"]
