# Event bus conformance

Rules every producer and consumer of the phantom event bus must follow. Read this before writing any code that emits or consumes bus events.

Before emitting or consuming bus events:

1. Fetch the current envelope schema — prefer MCP resource
   `contract://events/timeline-entry`; else `${BRAINBOX_URL}/openapi.json`
   (`components.schemas.AgentEnvelope`). Never code against a cached copy.
2. Publish only via outbox → `POST /api/agent_events` (`X-API-Key`). Never call
   a consumer directly.
3. Use bindings generated from `phantom-contracts` (pinned tag) — never hand-write
   the envelope.
4. Register new event types in `phantom-contracts/event-types.json`
   (`<domain>.<entity>.<action>`) before first emit.
