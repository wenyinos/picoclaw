# Memory schema v4 migration

PicoClaw 0.7.0 migrates long-term memory from schema v3 to v4 in one segment.
The old `v1 → v2 → v3` chain is gone on purpose: v1/v2 databases must be opened
once with a 0.6.x build, which upgrades them to v3, before this build accepts
them.

## What v4 adds

| Area | Objects |
|---|---|
| Ownership | `memory_domains`, `memory_actor_bindings`, `memory_processing_grants` |
| Entry governance | `entries.domain_id`, `row_revision`, `archived_from`, `event_start`, `event_end`, `sensitivity`, `qualifiers`, `qualifiers_hash`, `evidence_status`, `freshness` |
| Evidence | `sources.origin_event_id`, `origin_revision`, `confirmation_event_id`, `evidence_root`; `memory_origin_events` |
| Derived views | `memory_summary_evidence`, `memory_dependencies`, cluster/summary `generation` and domain columns |
| Durable work | `memory_jobs`, `memory_change_plans` |
| Companion | `memory_commitments`, `memory_recalls`, `memory_recall_feedback` |
| Forget safety | independent `memory/forget-ledger.db` with HMAC selectors and a recoverable target manifest |

## Preconditions

1. The database reports `schema_version = 3` (`sqlite3 memory.db "SELECT value FROM meta WHERE key='schema_version'"`).
2. No writer process is running. Stop the gateway/CLI before copying or migrating.
3. Disk space for a full `VACUUM INTO` snapshot (roughly the size of `memory.db`).

A v1/v2 database is rejected with an explicit error instead of being upgraded:
run a 0.6.x build once, verify `schema_version` is 3, then upgrade PicoClaw.

## How to migrate

**PicoClaw 0.7.2 and later never migrate during startup.** An existing v3
database — or a v4 database missing objects — opens in fail-closed mode: the
memory dashboard and the CLI still open it so the migration can run, but every
ordinary memory read/write returns `schema migration required`. A brand-new
database is bootstrapped directly at v4.

Run the explicit migration once:

```sh
picoclaw memory migrate
```

or use `/memory` → “Database migration” in the dashboard. The page shows the
current schema, the pending state, the backup path and the post-migration
counts before you confirm.

The migration:

1. Writes `memory.db.bak-<timestamp>` and `forget-ledger.db.bak-<timestamp>` under `memory/backups/` with `VACUUM INTO`.
2. Creates the v4 tables (`CREATE TABLE IF NOT EXISTS`).
3. Adds v4 columns with guarded `ALTER TABLE` statements.
4. Rebuilds `sources` field by field, preserving evidence text, model,
   confirmation flags, `message_id` and confidence. Missing old columns get
   explicit defaults; nothing that exists is dropped.
5. Creates the isolated `legacy_unassigned` domain.
6. Replaces the exact-fingerprint index with `(domain_id, content_hash)`.
7. Rebuilds the FTS projection if it is missing.
8. Writes `schema_version = 4` only after the whole segment succeeds.

Re-running is safe: every statement is idempotent, and a second run reports
zero changes.

## Verify

```sh
sqlite3 memory.db "SELECT value FROM meta WHERE key='schema_version'"   # 4
sqlite3 memory.db "PRAGMA integrity_check"                              # ok
sqlite3 memory.db "SELECT COUNT(*) FROM entries"                        # unchanged
sqlite3 memory.db "SELECT COUNT(*) FROM sources"                        # unchanged
picoclaw memory inspect <entry-id>                                      # sources keep evidence_text
picoclaw memory ledger-verify --backup memory/backups/forget-ledger.db.bak-<timestamp>  # match: true
```

`ledger-verify` compares the backup UUID/key_id with the active ledger; a mismatch exits non-zero and must not be used for forget replay.

## Rollback

- Application rollback means disabling the new capabilities, not downgrading
  the schema. A 0.6.x binary must not write a v4 database.
- Data rollback is offline: restore the database snapshot to a new path, then
  replay the latest `forget-ledger.db`. Restoring the snapshot alone can
  resurrect content that was forgotten after it was taken.
- Since 0.7.10, migration/repair backups include both the database and the
  ledger snapshots. Restore them as a pair and verify with
  `picoclaw memory ledger-verify --backup <ledger snapshot>`.
- Keep `forget-ledger.db` and its HMAC key with the database. A missing ledger
  or damaged identity disables destructive memory operations instead of silently
  creating an empty ledger or dropping obligations.

## Failure handling

| Failure | Behaviour |
|---|---|
| v1/v2 database | Refused with an explicit message; no partial write |
| Newer schema than the binary | Refused before any DDL |
| Interrupted migration | Transaction rolls back; next open retries |
| Missing v4 table/column | Healed on next open by the same segment |
| Missing ledger | Memory reads keep working; `forget` fails closed |
| Damaged ledger identity | Memory reads keep working; `forget`/`replay-forget` fail closed; identity is not reset |
| Journal written, main DB not cleaned | Ledger replay on startup |
| Main DB restored from backup | `POST /api/memory/ledger/replay` or `picoclaw memory replay-forget` |

## What is not migrated

- No owner is guessed from names, sessions or "there is only one user".
  Existing rows stay in `legacy_unassigned` until an owner claims them.
- Old rows without `origin_event_id` keep an empty origin: the migration never
  fabricates a message identity.
- The removed v3 data repairs (hard-coded entry reclassification, birthday
  restore) are not replayed. If a database still needs them, run 0.6.x first.
