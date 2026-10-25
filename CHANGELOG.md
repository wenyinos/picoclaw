# Changelog

## v0.4.0

### Added

- SQLite-backed long-term memory at `workspace/memory/memory.db`, with layered entries (`real/fictional/work/unknown`), relationship state, personas, boundaries, and source tracking.
- Memory deduplication: exact hashes, template folding, Chinese bigram similarity, SimHash near-duplicate detection, and conflict superseding.
- Memory review queue (`pending_review`) and human approval before uncertain memories enter prompts.
- Launcher **Memory** page (`/memory`) for overview, search, editing, review, relationship/persona, and settings.
- `picoclaw memory` CLI: `import`, `audit`, `stats`, `compact`, `export`, `render`.
- Relationship and persona memory APIs under `/api/memory/*`.
- One-shot import for 0.3.x workspaces: task records, session summaries, and legacy `MEMORY.md` bullets.
- Portable workspace support: relative `agents.defaults.workspace` resolved against `PICOCLAW_HOME`, no machine-specific paths stored in the memory database.

### Performance

- Pico streaming frames are throttled to limit intermediate snapshots.
- The final streaming frame is published before session persistence and compaction.
- Pico WebSocket writes have deadlines; slow clients can no longer block the agent worker indefinitely.
- Provider HTTP connections are pooled for concurrent sessions.
- Session JSONL appends are batched and written asynchronously; history writes stay off the response path.
- Tool definitions are reused within a turn.
- TTFT and chunk metrics are logged at INFO level.

### Fixed

- Data races in async sub-turn delivery, channel manager shutdown, and agent test helpers.
- Session API could read a JSONL file before buffered writes were flushed.
- Memory imports are idempotent; repeated imports no longer duplicate pending summaries or legacy tasks.
- Private memories are filtered by channel (`audience` + `private_channels`) before prompt injection.
- Legacy task templates and placeholder entries are folded or skipped during import.

### Changed

- `README.md` redesigned around 0.4.0; project entry documentation is maintained in English and Simplified Chinese only.
- Legacy `MEMORY.md` is no longer read at runtime when SQLite memory is enabled.
- 0.3.x configs without a `memory` section receive memory defaults automatically on load.

### Migration

0.3.x → 0.4.0 has **no automatic schema migration**. See [Migrating from 0.3.x to 0.4.0](docs/migration/0.4.0.md) and the [long-term memory guide](docs/guides/memory.md).

### Breaking changes

- Linux/mipsle binaries are no longer published: the pure-Go SQLite driver does not support MIPS32.
- Runtime memory state lives in `workspace/memory/memory.db`.
- Old `MEMORY.md` content must be imported once to be preserved.
- Old workspaces should be copied or imported while the gateway and launcher are stopped.
