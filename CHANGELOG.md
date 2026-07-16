# Changelog

## [0.2.0](https://github.com/neverprepared/phantom-skills/compare/v0.1.0...v0.2.0) (2026-07-16)


### Features

* agent-side MCP server, HTTP client, and offline write-queue (M2) ([bc599a0](https://github.com/neverprepared/phantom-skills/commit/bc599a0b6717abd9c83971560c769e73867471fe))
* pskillctl daemon core (M0 skeleton + M1 registry API) ([4a478fb](https://github.com/neverprepared/phantom-skills/commit/4a478fb5e0f3970c3e090cc845a07a60fde2db68))
* skill sync — materialize the shared registry into ~/.claude/skills (M3) ([a336100](https://github.com/neverprepared/phantom-skills/commit/a336100ed28abef3e498f6a97b6b611c37fd130f))
* telemetry, the proposal gate, and the pipeline seam (M4 + M5) ([4fd7f7f](https://github.com/neverprepared/phantom-skills/commit/4fd7f7fe2b906198fcbeaf43bbae28d34062e064))

## 0.1.0

Initial development release. The full plumbing layer of the self-improving
skills daemon:

- **Daemon** (`pskillctl server serve`): chi HTTP API under `/api/skills`,
  bearer→scope auth, Postgres System of Record, skills CRUD with
  content-addressed versioning, a human-gated create/prune/promote proposal
  queue, usage telemetry ingestion, and a `/sync` change-feed.
- **Agent** (`pskillctl client mcp`): stdio MCP server exposing `skill_list`,
  `skill_get`, `skill_sync`, `skill_usage_report`, and `skill_propose`, backed
  by an offline SQLite write-ahead queue.
- **Sync**: materializes promoted skills into `~/.claude/skills/<slug>/SKILL.md`
  with an ownership marker; hand-authored skills are never touched.
- **Pipeline seam**: `Detector/Author/Verifier/Pruner/Promoter` interfaces with
  a no-op default, plus phantom-brain integration and a Claude API wrapper
  scaffold. The intelligence algorithms land in a later release.
- **Deploy**: Dockerfile, docker-compose, and a Homebrew tap release pipeline.
