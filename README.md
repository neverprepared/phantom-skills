# phantom-skills (`pskillctl`)

A self-improving **skills daemon** for the phantom control-server fleet. It
watches an agent's real activity, detects when a reusable [Claude Code
skill](https://code.claude.com/docs/en/skills) would help, authors it, verifies
it actually helps (held-out grading), prunes stale/duplicate ones, and promotes
proven skills from a machine's local `~/.claude/skills/` up to a shared registry
that syncs back down to every machine.

Every create/prune/promote is **human-gated by default**, with per-action-type
autonomous opt-in earned once the gate's approval rate proves the automated
standard is trustworthy.

It's a meta-skill: it exists to keep the skill library small, sharp, and shared
— so context loads what's needed and nothing that's rotted.

## Why

Claude Code loads skills via progressive disclosure — ~100 tokens of each
skill's `description` sit in context always, and the full body loads only when
the model matches a task to that description. That keeps the window small, but
today skills are hand-authored, never pruned, and never shared across machines.
A survey of ~25 prior systems (Voyager, TROVE, Ada, ExpeL, Anthropic
`skill-creator`, `crune`, …) found nobody ships the full combination:
continuous background *watching* + usage-and-success *pruning* + automatic
cross-agent *promotion* + held-out *verification*, with a light human-in-loop.
That combination maps directly onto phantom-brain's promote-to-shared-memory
model.

## Architecture

One Go binary, two roles (mirrors `pbrainctl`):

- `pskillctl client mcp` — per-session stdio MCP server spawned by Claude Code.
  Exposes the `skill_*` tools, tails the session transcript, syncs skills into
  `~/.claude/skills/`, and proxies reads/writes to the daemon over HTTP (with a
  local SQLite write-ahead queue for offline resilience).
- `pskillctl server serve` — the control-server HTTP daemon. Owns the shared
  skill registry (Postgres SoR), runs the background pipeline
  (detect → author → verify → prune → promote), and records every decision back
  into phantom-brain.

```
[Claude Code session]                          CONTROL SERVER
  pskillctl client mcp  ──HTTP /api/skills──►  pskillctl server serve
   ├─ tail session JSONL                        ├─ chi HTTP API + bearer auth
   ├─ SQLite wqueue                             ├─ Postgres (pgvector) registry
   └─ syncer → ~/.claude/skills/…/SKILL.md      ├─ pipeline workers
                                                └─ brainlink → phantom-brain
```

## Status

Early build. See the milestone plan for what's landed vs pending. `M0`
(skeleton) is the compiling cobra tree; the daemon, sync, and pipeline arrive in
later milestones.

```
make build      # -> ./pskillctl
./pskillctl client version
./pskillctl --help
```
