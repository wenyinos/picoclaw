# Long-term memory (SQLite)

PicoClaw stores long-term memory in SQLite instead of a free-form `MEMORY.md`. The database is the only runtime source. `MEMORY.md` is an optional, regenerable export: PicoClaw never reads it at runtime, including when `memory.enabled` is `false`.

## Portability

- The workspace contains no machine-specific absolute paths.
- `agents.defaults.workspace` may be relative (`"workspace"`); it is resolved against `PICOCLAW_HOME`.
- Stop the process and checkpoint the WAL before copying. Only `memory/memory.db` is required.
- After copying, run `chown -R <user>:<group> workspace` for the service account.

## Directory layout

```text
workspace/
├── memory/
│   ├── memory.db          # SQLite database (WAL)
│   ├── MEMORY.md          # optional export (never read at runtime)
│   └── import-report.md   # one-shot import report
└── sessions/              # raw session history, unchanged
```

## Configuration

```json
"memory": {
  "enabled": true,
  "db_path": "memory/memory.db",
  "budget_tokens": 1200,
  "auto_extract": true,
  "extract_model": "",
  "private_channels": ["pico"],
  "inject_fictional": false,
  "review_required": true,
  "episodic_retention_days": 365
}
```

- `enabled`: disables long-term memory entirely when `false`; there is no Markdown fallback.
- `budget_tokens`: total injection budget for fixed and retrieved blocks. `boundary` rules are appended outside the fixed-block budget; they are not trimmed by it, but still obey audience, private-channel, and fictional filtering.
- `auto_extract`: run rule-based extraction after each turn.
- `review_required`: uncertain memories enter `pending_review` and are not injected.
- `private_channels`: memories with `audience=private` are only injected into these channels.
- `inject_fictional`: whether role-play memories may enter real conversations (off by default).

## Memory model

| Field | Values | Meaning |
|---|---|---|
| `realm` | real / fictional / work / unknown | real user facts / role-play / procedures / pending classification |
| `layer` | persona / profile / relationship / episodic / procedural / boundary | character / profile / relationship / event / procedure / boundary |
| `status` | active / pending_review / superseded / archived | lifecycle |

`relationship_state` stores names, stage, intimacy, anniversaries, and boundaries as versioned current values. `personas` stores versioned character definitions. `boundary` rules are not part of the fixed-block budget: they are appended as a safety block so a long profile or relationship section cannot evict them.

## Automatic extraction

After a turn, only explicit user statements are considered: `记住/记一下/别忘了`, preferred names, preferences/allergies, birthdays/anniversaries, and dated events. Ordinary chat, role-play keywords (`设定/扮演/剧情/人设/世界观/虚构/故事线`), and assistant replies are never auto-saved.

## Deduplication and conflicts

Writes are processed in order:

1. Normalize and check the SHA256 exact hash.
2. Fold known templates (for example repeated legacy extraction prompts).
3. Merge near-duplicates using Chinese bigram Jaccard ≥ 0.75 or SimHash Hamming distance ≤ 3.
4. Supersede conflicting values with the same `realm+subject+predicate`.
5. Send `unknown` entries to review.

## Search semantics

The `memory` tool's search is an administrative search: it covers all layers by default, supports `realm` / `layer` / `status` filters, and can see private memories. Prompt injection uses a narrower retrieval path: active episodic/procedural only, relevance-filtered, with audience, private-channel, and fictional isolation still applied. The fixed block itself is still trimmed to its budget; `boundary` is appended after it and is not part of that trim.

## Dashboard

Open `/memory` in the launcher:

- Overview: counts, database size, relationship state, archival action.
- Memory library: server-side pagination (100 per page), search, filter, edit, archive, restore.
- Review: confirm real, mark fictional, mark work, ignore.
- Relationship and persona: names, stage, anniversaries, boundaries, persona versions.
- Settings: budget, auto-extraction, private channels, import.
- Database repair: preview and bulk-reclassify by realm / layer / status / audience / keyword; a SQLite `VACUUM INTO` backup is created before applying.

## CLI

```bash
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render   # optional Markdown export, never read back
```

`memory import` is the only command that reads a legacy `MEMORY.md`; the runtime never does.

## Migrating from 0.3.x

There is no runtime schema migration. New versions only read `memory.db`:

1. Stop the gateway and launcher.
2. Back up the old `workspace/memory`.
3. Use the new workspace and run `memory import` once, or import before replacing.
4. Start the service and review pending entries at `/memory`.

## API

```text
GET    /api/memory/overview
GET    /api/memory/entries
POST   /api/memory/entries
GET    /api/memory/entries/{id}
PUT    /api/memory/entries/{id}
POST   /api/memory/entries/{id}/archive
POST   /api/memory/entries/{id}/restore
GET    /api/memory/review
POST   /api/memory/review/{id}/resolve
GET    /api/memory/relationship
PUT    /api/memory/relationship
GET    /api/memory/persona
PUT    /api/memory/persona
GET    /api/memory/settings
PUT    /api/memory/settings
POST   /api/memory/compact
POST   /api/memory/repair/preview
POST   /api/memory/repair/apply
POST   /api/memory/import
GET    /api/memory/export
GET    /api/memory/stats
```
