# Changelog

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
