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

`relationship_state` stores names, stage, intimacy, anniversaries, and boundaries as versioned current values. `personas` stores versioned character definitions. `predicate` uses a controlled vocabulary and `facet` distinguishes different sides of the same predicate; only scalar profile facts or an explicit `supersedes_id` automatically replace old values. `boundary` rules are not part of the fixed-block budget: they are appended as a safety block so a long profile or relationship section cannot evict them.

Schema v3 adds:

| Field | Meaning |
|---|---|
| `event_time_precision` | exact / date / approximate / range / narrative / planned / unknown |
| `event_timezone` / `event_time_source` | IANA zone; user_stated / model_inferred / imported / corrected |
| `valid_from` / `valid_to` | validity interval, so corrected facts keep their history |
| `pinned_core` | protected rules that automatic writes cannot supersede |
| `sources.evidence_text` | the sentence a memory was extracted from |
| `memory_relations` | typed edges (supersedes / contradicts / related_to / caused_by / influenced_by / fulfilled_by / next_scene / part_of) |
| `memory_operations` | governance log; forget entries store a content hash, never text |

## Automatic extraction

After a turn, only explicit user statements are considered: `记住/记一下/别忘了` at the start of a sentence, preferred names, preferences/allergies, birthdays/anniversaries, and dated events. Ordinary chat, role-play keywords (`设定/扮演/剧情/人设/世界观/虚构/故事线`), and assistant replies are never auto-saved.

The write firewall runs before those branches:

- A sentence that looks like dialogue or a setting document (quotes or `【...】` covering a meaningful share of the text) is skipped entirely.
- `记住` only counts at the beginning of a sentence, so “记住，我所有的反抗…” inside role-play does not become a profile fact.
- Name extraction ignores matches inside quotes and interrogatives such as 「你叫我什么？」.
- Emotional one-liners (`今天好累`, `抱抱`, `想你了`) only update `current_mood` with a 24-hour TTL; they do not create entries.

## Deduplication and conflicts

Writes are processed in order:

1. Normalize and check the SHA256 exact hash.
2. Fold known templates (for example repeated legacy extraction prompts).
3. Merge near-duplicates using Chinese bigram Jaccard ≥ 0.75 or SimHash Hamming distance ≤ 3.
4. Supersede conflicting values with the same `realm+subject+predicate`.
5. Send `unknown` entries to review.

## Search semantics

The `memory` tool's search is an administrative search: it covers all layers by default, supports `realm` / `layer` / `predicate` / `facet` / `status` filters, and can see private memories. Prompt injection uses a narrower retrieval path: active relationship/episodic/procedural only, relevance-filtered, with audience, private-channel, and fictional isolation still applied. At most two relationship entries are injected per turn so long relationship notes cannot crowd out events and procedures.

Both paths share the hybrid candidate set:

1. **Anchor** — exact/contains match on `facet`, `predicate`, `subject`, or a first-line `【...】` title. A pinned entry that hits an anchor is sorted first unconditionally.
2. **FTS5** — phrase match for queries of three or more runes. A build without the trigram tokenizer falls back to `unicode61`.
3. **LIKE** — one- and two-character Chinese queries, and a recall fallback when FTS returns fewer than five hits.
4. **Temporal** — relative-time phrases become an interval that overlaps `event_at` or `valid_from/valid_to`.
5. **1-Hop** — related/caused/fulfilled/next-scene edges from the top seeds; targets are re-checked for status, realm, scope and audience.

Fixed, boundary, and retrieval blocks are accounted separately. The boundary block has its own cap (`max(200, fixed_budget*30%)`), always keeps at least one rule, and is included in the total instead of silently doubling it.

## Database migration (schema v4)

0.7.0 upgrades `memory.db` from v3 to v4 in a single segment; the old v1 → v2 → v3 chain was removed. A v1/v2 database is rejected with an explicit error and must be opened once with a 0.6.x build first. v4 adds ownership domains and actor bindings, processing grants, origin events, dependency/job/plan tables, summary evidence, commitments and recalls, and the independent forget ledger.

The migration is one guarded segment:

1. Creates the v4 tables (`CREATE TABLE IF NOT EXISTS`).
2. Adds the v4 columns with a precomputed existence plan.
3. Rebuilds `sources` field by field, preserving `evidence_text`, model, prompt version, confidence, confirmation flags and `message_id`; missing old columns get explicit defaults.
4. Creates the isolated `legacy_unassigned` domain and replaces the exact-fingerprint index with `(domain_id, content_hash)`.
5. Writes `schema_version=4` only after the whole segment succeeds, in the same transaction.

Other guarantees:

- A `VACUUM INTO` backup is written to `memory/backups/`; the newest two are kept.
- Re-running is safe and reports zero changes on a current database.
- FTS5 is probed before the transaction; a missing projection is rebuilt, and a build without FTS5 records `none` instead of failing to open.
- Missing v4 tables/columns are healed by the same idempotent segment.
- The removed v3 data repairs (hard-coded reclassification, birthday restore) are not replayed; run 0.6.x if a database still needs them.

`forget-ledger.db` lives next to `memory.db`, uses its own HMAC key, and must be backed up with the database. Losing the ledger disables destructive memory operations instead of silently dropping obligations.

Run it from `/memory` → “Database migration” or:

```bash
picoclaw memory migrate
```

Full procedure: [Memory schema v4](../reference/memory-schema-v4.md).

## Dashboard

Open `/memory` in the launcher:

- Overview: counts, database size, relationship state, archival action.
- Database migration: schema version, empty predicates, relationship count, pinned core, relation edges, FTS rows and tokenizer, one-click backup and migration. The same button handles v1 / v2 → v3.
- Memory library: server-side pagination (100 per page), search, filter, edit, archive, restore; fixed table columns and an independently scrollable detail panel.
- Review: confirm real, mark fictional, mark work, ignore.
- Relationship and persona: names, stage, anniversaries, boundaries, persona versions; persona realm / audience / scope.
- Settings: budget, auto-extraction, private channels, import.
- Database repair: preview and bulk-reclassify by realm / layer / status / audience / keyword; a SQLite `VACUUM INTO` backup is created before applying.

## CLI

```bash
picoclaw memory migrate
picoclaw memory repair --dry-run
picoclaw memory inspect <id>
picoclaw memory rebuild-fts
picoclaw memory forget <id> [--yes]
picoclaw memory replay-forget
picoclaw memory capsule --preset t0_compact
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render   # optional Markdown export, never read back
```

`memory import` is the only command that reads a legacy `MEMORY.md`; the runtime never does. `memory forget` without `--yes` is a dry run. `memory replay-forget` re-applies forget tombstones after restoring an older backup.

## Migrating to 0.7.0

0.3.x → 0.4.x used a one-time `memory import` to move the old `MEMORY.md` into SQLite. 0.4.x → 0.5.x upgraded to schema v3 on first open. 0.7.0 upgrades v3 → v4; a v1/v2 database must pass through a 0.6.x build first, because the old migration chain is no longer shipped.

1. Stop the gateway and launcher.
2. Back up the old `workspace/memory` including `forget-ledger.db` once it exists.
3. Start 0.7.0 and run the migration from `/memory` → “Database migration”; for a 0.3.x workspace, run `memory import` once first, and for a pre-v3 database run a 0.6.x build once.
4. Verify with `PRAGMA integrity_check`, `picoclaw memory audit`, and entry counts; check that a public channel cannot see a `fictional/private` persona.

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
GET    /api/memory/migration/status
POST   /api/memory/migration/apply
GET    /api/memory/ops
GET    /api/memory/capsule/{id}
POST   /api/memory/import
GET    /api/memory/export
GET    /api/memory/stats
```
