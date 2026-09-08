# CLAUDE.md

Guidance for Claude Code working in this repository.

## Overview

**phantom-skills** (`pskillctl`) is a single Go binary that runs a self-improving
Claude Code *skills* registry for the phantom fleet. It has two roles:

- **Agent side** (`pskillctl client …`) — a stdio MCP server spawned per Claude Code
  session, plus local sync/inspection commands. Talks to the daemon over HTTP; never
  touches Postgres.
- **Daemon side** (`pskillctl server …`) — the control-server HTTP API under
  `/api/skills`, owning the shared registry in Postgres, the human-gated proposal
  queue, and the background pipeline worker loop.

Module: `github.com/neverprepared/phantom-skills` (Go 1.26.2). CGO is required
(`mattn/go-sqlite3` for the agent write-ahead queue); no build tags are needed.

## Architecture

```
[Claude Code session]                        CONTROL SERVER
 pskillctl client mcp ──HTTP /api/skills──► pskillctl server serve
  ├─ SQLite wqueue (offline writes)          ├─ chi router + bearer→scope auth
  └─ syncer → ~/.claude/skills/<slug>/       ├─ Postgres SoR (pgx + golang-migrate)
                                             ├─ pipeline worker loop (seam)
                                             ├─ brainbox → ratchet workers
                                             └─ brainlink → phantom-brain
```

Packages (`internal/`):

| Package | Role |
| --- | --- |
| `mcp` | Registers the `skill_*` MCP tools; thin glue over injected deps. |
| `client` | Agent HTTP client, connectivity tracker, wqueue drainer. |
| `client/wqueue` | SQLite write-ahead queue: atomic enqueue, backoff, dead-letter. |
| `config` | Agent contract loaded from `CL_SKILLS_*` env vars. |
| `syncer` | Materializes promoted skills into the skills dir. |
| `skillfile` | Shared `SKILL.md` codec + canonical content SHA. |
| `server` | Daemon lifecycle, chi API, auth registry, handlers, pipeline seam, workers, improver. |
| `pgstore` | Postgres System of Record: skills, versions, usage, proposals, ratchet runs. |
| `nodesync` | P2P merge engine replicating the immutable `skill_versions` op-log. |
| `brainbox` | Client for the brainbox agent-execution plane (fires ratchet workers). |
| `brainlink` | Best-effort writes of decisions/telemetry into phantom-brain. |
| `version` | Build metadata injected via `-ldflags`. |

`migrations/` holds numbered up/down SQL (embedded via `migrations/embed.go`):
`skills`, `skill_versions`, `usage_events`, `proposals`, `promotion_state`,
`ratchet_runs`, and the 0007 P2P substrate.

## Key commands

```
make build      # -> ./pskillctl (injects version/commit/date via -ldflags)
make test       # go test -count=1 -timeout=90s ./...
make test-race  # go test -race
make vet        # go vet ./...   (this is the lint gate; there is no golangci-lint)
make fmt        # gofmt -s -w .
make tidy       # go mod tidy
make all        # vet + test + build
make sqlc       # regenerate pgstore from migrations (NOT part of `all`)
```

CI (`.github/workflows/go.yml`) runs vet → build → test → test-race on
`macos-14` and `ubuntu-latest`. Releases are release-please + `release.yaml`
(native-runner matrix, Homebrew tap publish).

Runtime:

```
pskillctl client mcp                  # stdio MCP server (spawned by Claude Code)
pskillctl client sync [--dry-run|--reset|--skills-dir]
pskillctl client skill list|show <name>|diff <name>
pskillctl client queue list|drain|purge
pskillctl server serve
pskillctl server config validate
pskillctl server db migrate|status
pskillctl server registry list|add <scope>|token <scope>
pskillctl server proposal list|approve <id>|reject <id>
pskillctl version
```

## MCP tools exposed (`pskillctl client mcp`)

Five tools, no resources or prompts:

- `skill_list` — list registry skills for the scope (`status`, `tag`, `limit`).
- `skill_get` — fetch one skill's full `SKILL.md` by `name`.
- `skill_sync` — pull promoted skills into the local skills dir.
- `skill_usage_report` — record `invoked|helpful|ignored|error` for a skill.
- `skill_propose` — submit a create/edit proposal (human-gated).

Reads (`skill_list`, `skill_get`) are online-only and fail clearly when the daemon
is unreachable. Writes (`skill_usage_report`, `skill_propose`) go through the
wqueue and degrade to "queued" offline.

## HTTP API (daemon)

Mounted at `/api/skills`. `GET /health` is unauthenticated; everything else needs
a bearer token bound to a `(profile, skillset)` scope.

```
GET    /whoami
GET    /skills            POST /skills
GET    /skills/{name}     PUT  /skills/{name}     DELETE /skills/{name}
GET    /skills/{name}/versions
GET    /sync?since=<cursor>          # agent change feed (+ retired deletes)
POST   /usage
GET    /proposals         POST /proposals
GET    /proposals/{id}
POST   /proposals/{id}/approve       POST /proposals/{id}/reject
```

## Configuration

**Daemon** — `server.toml` in `PSKILLS_CONFIG_DIR` (default
`~/.config/phantom-skills-server`); state in `PSKILLS_DATA_DIR` (default
`/var/lib/phantom-skills`). Blocks: `[server]`, `[postgres]` (env override
`PSKILLS_POSTGRES_DSN`; empty ⇒ skills CRUD returns 503), `[brain]` (env
`PSKILLS_BRAIN_TOKEN`), `[pipeline]`, `[brainbox]` (env `PSKILLS_BRAINBOX_KEY`),
`[registry]`, `[defaults]`. Example: `docker/config-example/server.toml`.

**Agent** — env only: `CL_SKILLS_API`, `CL_SKILLS_API_TOKEN`,
`CL_WORKSPACE_PROFILE`, `CL_SKILLS_SET`, optional `CL_SKILLS_DIR` /
`PSKILLS_STATE_DIR`.

## Conventions

- **Ownership invariant (safety-critical):** the syncer only writes or removes dirs
  carrying the `x-phantom-skills` frontmatter marker. A hand-authored skill without
  the marker is never overwritten or pruned. Do not weaken this.
- **The pipeline seam:** `internal/server/pipeline.go` defines `Detector`, `Author`,
  `Verifier`, `Pruner`, `Promoter`. The daemon ships with the **zero-value
  `Pipeline` — every member is nil, so the worker loop is a no-op.** Plumbing must
  stay ignorant of the algorithms; keep implementations behind these interfaces.
- **Human-gated by default:** `auto_approve_below_risk = 0` keeps every
  create/prune/promote in the proposal queue. The PR opened by a ratchet worker is
  the gate for the auto-fire path.
- **brainlink writes are best-effort:** a phantom-brain outage must never fail a
  skills operation — log and swallow.
- **`internal/skillfile` is a leaf package** (no daemon/client deps) so both sides
  agree on the format and content SHA.
- Generated `pgstore` code is checked in; rerun `make sqlc` only after editing
  migrations or queries.
- Integration tests self-skip unless gated env is set: `PSKILLS_TEST_DSN`
  (pgstore), `PS_TEST_BASE_DSN` (nodesync), `BRAINBOX_LIVE=1` (live brainbox).
  `make test` stays hermetic.
- Emitting or consuming bus events? Follow
  [docs/event-bus-conformance.md](docs/event-bus-conformance.md) — envelope schema
  from `phantom-contracts`, publish only via outbox `POST /api/agent_events`.
- Commits follow Conventional Commits (release-please drives versioning).
