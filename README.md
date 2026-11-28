<p align="center">
  <img src="assets/logo.webp" alt="PicoClaw" width="360">
</p>

<p align="center">
  <strong>Ultra-lightweight personal AI assistant, written in Go.</strong><br>
  Long-term memory, a companion-oriented dashboard, and a single portable workspace.
</p>

<p align="center">
  <a href="docs/project/README.zh.md">简体中文</a> ·
  <a href="https://wenyinos.github.io/picoclaw/">Online docs</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/github/v/release/wenyinos/picoclaw?label=Version&color=blue" alt="Version">
  <img src="https://img.shields.io/badge/Arch-x86__64%20%7C%20ARM64%20%7C%20RISC--V%20%7C%20LoongArch-blue" alt="Architectures">
  <img src="https://img.shields.io/badge/License-MIT-green" alt="License">
</p>

---

## A personal assistant that remembers

PicoClaw is a small, self-hosted AI assistant that runs on a laptop, a home server, or a $10 board. It connects to the chat apps and model providers you already use, executes tools, and keeps a workspace you own.

**0.7.25 is the current stable release.** The 0.7 line turns the memory V4 foundation into a governed, fail-closed memory system: ownership domains, an independent forget ledger with backups, evidence-bound summaries, durable per-backend cleanup receipts, and a send-point egress gate that also covers `/btw`, subagent calls and background summaries. Instead of asking the model to rewrite a Markdown file, PicoClaw keeps structured memories in SQLite, deduplicates them, separates real facts from role-play, and injects only what is relevant to the current conversation.

### 0.7 progress

| Version | Status | Main work |
|---|---|---|
| 0.7.0 | released | Schema v4, domains/bindings/grants, independent forget ledger, source events, summary evidence, commitments/recalls, durable jobs |
| 0.7.1 | released | Initial egress gate for fallback/streaming, JSONL forget cleanup, governance dashboard |
| 0.7.2 | released | Explicit migration; v3/missing-object v4 databases open fail-closed |
| 0.7.3 | released | Private-channel opt-out semantics; group/public chats skipped by extraction |
| 0.7.4 | released | Deprecated `private_channels` compatibility; fixes the 0.7.3 startup block |
| 0.7.10 | released | Chat-kind enforcement across prompts/tools, tool governance, storage-level secret rejection, single-provider egress, ledger backup/verify, FTS/LIKE permission before `LIMIT`, Correct/summary/cleanup consistency |
| 0.7.20 | released | Correct tombstone enforcement, owner-only model governance, strict write DTOs, audit actors/request IDs, atomic fresh bootstrap and repair backup, Seahorse conversation isolation and per-message cleanup, evidence roots, full egress send-point coverage, pre-`LIMIT` TTL/layer filtering |
| 0.7.25 | current stable | Replay blocked/revision gates, recovery readiness gate, per-locator ledger receipts, successor cleanup jobs, origin-event production wiring, durable extraction references, realm/import isolation, Seahorse race fix, budget/LIKE/egress corrections, OpenAPI 3.1 |

There is no main-database schema change after v4; 0.7.25 adds internal forget-ledger receipt tables and recovery gates. Remaining boundaries are explicit: the full persistent MemoryUseManifest, temporary no-memory sessions, external LLM extraction, embeddings/vector retrieval, MMR, proactive delivery scheduling, full multi-domain relationship support, and long-document chunking/time extraction are not wired.

### Memory that lasts

- **SQLite memory store** at `workspace/memory/memory.db`, with exact and near-duplicate detection, conflict superseding, and an evidence chain for every write.
- **Layered memory**: user profile, relationship state (names, stage, anniversaries, boundaries), episodic events, procedural knowledge, and versioned personas.
- **Real vs. fictional isolation**: role-play and story settings are stored separately and are never mixed into the user's real profile. A write firewall keeps dialogue and setting documents out of `real/profile`.
- **Human review queue**: uncertain memories wait in `pending_review` and are not injected until approved.
- **Governance loop**: `inspect / confirm / correct / archive / restore / forget`. Content changes create a new version, pinned core memories refuse silent overwrites, and forgetting also removes the relationship-state copies (nicknames, birthdays) that feed the prompt block.
- **Forget ledger**: `forget` journals the obligation to an independent `forget-ledger.db` before touching the database. Restoring an older backup does not resurrect forgotten content; `replay-forget` now follows manifest entry IDs plus permission domains instead of a database-wide hash scan. A missing or damaged ledger keeps reads available but fails `forget` closed, and migration/repair backups include both the database and the ledger. `memory forget-audit` reports historical operations without deleting anything.
- **Permission domains and chat kinds**: entries carry a `domain_id`; dedupe, auto-replace and capsule reads never merge different domains. Group/public chats only see audience `""`/`all` in prompts and the `memory` tool, even on channels that are otherwise private.
- **Governed model tools**: models keep a controlled allowlist (`add / search / inspect / non-core correct / legacy capsule / forget preview`) governed by `MemoryService`. Confirmation, archive/restore, relationship updates and any force/pinned/boundary change are owner-only. Passwords, verification codes, API keys and private keys are rejected at `Store.Add`, not at one caller.
- **Evidence-bound summaries**: manual topic/stage clusters, one claim per source, a generation + membership check before publishing, and invalidate-on-change. Summaries are revocable views, never authoritative facts.
- **Time semantics**: today/yesterday/last week/last summer are parsed deterministically into `date / range / approximate / planned`; approximate phrases never get a fabricated day.
- **Hybrid retrieval**: anchor (facet/predicate/title), FTS5, LIKE, temporal overlap, and one-hop relation expansion. Visibility is applied in SQL before `LIMIT`, including the FTS and LIKE channels, so an invisible row cannot starve a visible one.
- **Scene capsules**: `load_capsule` returns the T0 writing rules in one call (full or compact); capsules use the real request channel and return empty for public/group chats.
- **Real budget accounting**: fixed, boundary and retrieval blocks are counted separately, and `memory audit` prints the measured tokens.
- **Schema v4 migration**: one explicit segment from v3 to v4 with `VACUUM INTO` snapshots of both the database and the ledger. Databases older than v3 must be upgraded with 0.6.x first; see [Memory schema v4](docs/reference/memory-schema-v4.md).
- **Egress gate**: when `memory.allowed_chat_recipients` is non-empty, every actual provider call that can carry memory or session-derived content — primary, fallback, retry, streaming, `/btw`, subagent loops and background summaries — must match the list or return the non-retryable `memory egress denied`. A policy denial stops the chain instead of silently switching providers.
- **Cross-store forget cleanup**: JSONL and Seahorse both verify complete source roots before deletion; duplicate messages, tool-call text and summaries derived from a forgotten message are covered. Unverifiable fragments remain `unavailable/partial` rather than being guessed.
- **Channel visibility (opt-out)**: private memories are visible in every channel by default; exclude the ones you do not want in the settings page (checkboxes, all checked by default). Group/public chats never see them in prompts or tools.

### A dashboard for the relationship

The launcher includes a dedicated **Memory** page at `/memory`:

- Overview of entry counts, database size, relationship state, and pending reviews.
- Database migration: schema version, empty predicates, relationship count, FTS rows and tokenizer, one-click backup and upgrade.
- Search and edit memories by realm, layer, and status.
- Review queue to confirm real facts, mark fiction, or archive noise.
- Relationship and persona editor with version history.
- Settings for budget, auto-extraction, private channels, and retention.
- One-shot import of a 0.3.x workspace.

### Workspace you can move anywhere

- The workspace contains no machine-specific absolute paths.
- `agents.defaults.workspace` accepts a relative value such as `"workspace"`, resolved against `PICOCLAW_HOME`.
- `memory.db` and `forget-ledger.db` are self-contained; stop the process, copy the memory directory, and start it on another machine. Restore the pair together.
- Sessions are append-only JSONL and are written asynchronously, so saving history stays off the response path.

### Faster, quieter responses

The 0.5 streaming work also tightened the streaming path: intermediate Pico frames are throttled, the final frame is published before session persistence and compaction, WebSocket writes have deadlines, provider connections are pooled, and session appends are batched. On a local Pico WebSocket test the first visible frame arrives in under a millisecond; model time dominates.

### Runs almost anywhere

- x86_64, ARM64, ARMv7, RISC-V, LoongArch.
- Linux/mipsle is not published: the pure-Go SQLite driver does not support MIPS32.
- Linux, macOS, Windows, Android/Termux, Docker.
- OpenAI-compatible APIs, Anthropic, Gemini, Bedrock, Azure, local models, and more.
- Telegram, Discord, Slack, Feishu, WeCom, QQ, Matrix, IRC, MQTT, and a native Pico WebSocket UI.

---

## Core capabilities

The 0.7 memory work sits alongside PicoClaw's original assistant features:

- **Chat channels** — Telegram, Discord, Slack, Feishu/Lark, WeCom, WeChat, QQ, DingTalk, LINE, Matrix, IRC, MQTT, VK, and the native Pico WebSocket UI. See [Chat apps](docs/guides/chat-apps.md).
- **Model providers** — OpenAI-compatible APIs, Anthropic, Gemini, Bedrock, Azure, OAuth providers, and local runtimes; fallback chains and a light model for side questions. See [Providers](docs/guides/providers.md).
- **Tools** — file read/write/edit, shell execution, web search/fetch, messaging, media and image handling, scheduling, and sub-agents. See [Tools](docs/reference/tools_configuration.md).
- **MCP** — connect external Model Context Protocol servers and expose their tools to the agent. See [MCP CLI](docs/reference/mcp-cli.md).
- **Skills** — workspace, global, and builtin skill sources; search, install, and use skills from chat. See [Configuration](docs/guides/configuration.md).
- **Automation** — cron jobs with live CLI reload, direct delivery, shell commands, and visible failure state; heartbeat tasks, hooks, and optional agent self-evolution. See [Cron](docs/reference/cron.md), [Hooks](docs/architecture/hooks/README.md), and [Agent evolution](docs/architecture/agent-self-evolution.md).
- **Sub-agents** — async background work and long-running task orchestration. See [Spawn and async tasks](docs/guides/spawn-tasks.md).
- **Interfaces** — CLI (`onboard`, `agent`, `gateway`), desktop launcher, Docker, and Android/Termux. See [Docker](docs/guides/docker.md) and [Termux](docs/guides/android-termux.md).
- **Memory** — SQLite long-term memory, relationship/persona state, review queue, cross-layer search, repair dashboard, and CLI. See [Long-term memory](docs/guides/memory.md).
- **System prompt** — a global operator instruction layer managed from the launcher and applied to every channel without a restart. See [Configuration](docs/guides/configuration.md).

---

## Quick start

### Use a release binary

```bash
picoclaw onboard
picoclaw gateway
# optional desktop UI
picoclaw-launcher
```

Open `http://localhost:18800` for the launcher. The memory page is at `/memory`.

### Build from source

Requirements: Go 1.25+, Node.js 20+, pnpm, make.

```bash
git clone https://github.com/wenyinos/picoclaw.git
cd picoclaw

make build              # terminal/gateway binary
make build-launcher     # desktop launcher with embedded Web UI
```

Artifacts are written to `build/`.

If you invoke Go directly, include the project build tags:

```bash
CGO_ENABLED=0 go build -tags goolm,stdjson ./...
```

`goolm` selects mautrix's pure-Go crypto implementation. Without it, the default libolm backend requires CGO and a system libolm installation, so a `CGO_ENABLED=0` build fails with an empty `crypto/libolm` package.

---

## Memory configuration

```json
{
  "agents": {
    "defaults": {
      "workspace": "workspace"
    }
  },
  "memory": {
    "enabled": true,
    "db_path": "memory/memory.db",
    "budget_tokens": 1200,
    "auto_extract": true,
    "private_channel_exclusions": [],
    "inject_fictional": false,
    "review_required": true,
    "episodic_retention_days": 365
  }
}
```

Older 0.3.x configs do not need a `memory` section: loading them applies these defaults automatically. Use `"workspace": "workspace"` to keep the configuration portable across machines and users.

The SQLite database is the only runtime memory source. PicoClaw never reads `MEMORY.md` or daily notes at runtime, including when `memory.enabled` is `false`; disabling memory means no long-term memory is injected, not a Markdown fallback.

### Memory CLI

```bash
picoclaw memory migrate
picoclaw memory repair --dry-run
picoclaw memory inspect <id>
picoclaw memory rebuild-fts
picoclaw memory forget <id> --yes
picoclaw memory replay-forget
picoclaw memory cleanup
picoclaw memory ledger-verify --backup <path>
picoclaw memory capsule --preset t0_compact
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render
```

See [Long-term memory](docs/guides/memory.md) for the data model, deduplication rules, review workflow, and API.

---

## Migrating to 0.7.x

0.7 keeps `workspace/memory/memory.db` as the source of truth and upgrades it from schema v3 to v4 in one segment. **0.7.2 and later never migrate on startup**: an existing v3 database opens in fail-closed mode (`schema migration required` on reads/writes) until you run `picoclaw memory migrate` or use `/memory` → “Database migration”. Databases older than v3 are rejected: open them once with a 0.6.x build to reach v3 first. See [Memory schema v4](docs/reference/memory-schema-v4.md) for the full matrix.

1. **Stop the services**

   ```bash
   systemctl --user stop picoclaw picoclaw-launcher
   ```

2. **Back up the workspace**

   ```bash
   cp -a ~/.picoclaw/workspace ~/.picoclaw/workspace.backup-$(date +%F)
   ```

3. **Start 0.7.x and open the migration page**

   Memory reads and writes stay disabled until the explicit migration runs. Open `http://localhost:18800/memory` → “Database migration” and run it.

   ```bash
   systemctl --user start picoclaw picoclaw-launcher
   ```

   Open `http://localhost:18800/memory` → “Database migration” and run the migration. The CLI equivalent is:

   ```bash
   picoclaw memory migrate
   ```

   The migration is idempotent. It adds the v4 governance columns and tables, rebuilds `sources` field by field, creates the isolated `legacy_unassigned` domain, replaces the exact-fingerprint index with a per-domain one, rebuilds FTS when missing, and writes rotated `memory/backups/memory.db.bak-*` and `forget-ledger.db.bak-*` snapshots (the newest two of each are kept). The removed v1/v2/v3 repairs are not replayed.

4. **Verify**

   ```bash
   sqlite3 ~/.picoclaw/workspace/memory/memory.db "PRAGMA integrity_check;"
   ```

   Also check the entry counts and that a public channel cannot see a `fictional/private` persona.

5. **0.3.x workspaces only**

   Run `picoclaw memory import --from ~/.picoclaw/workspace` once before or after the upgrade. The old `MEMORY.md` is never read at runtime.

Notes:

- The v0.4.6 tag remains available; install 0.7.25 to get schema v4, the fail-closed forget ledger, per-locator cleanup receipts, recovery readiness gating and the Launcher migration page.
- After restoring a database backup, verify the ledger pair with `picoclaw memory ledger-verify --backup <forget-ledger snapshot>` before replaying forgets. Check `picoclaw memory recovery-status` before enabling long-term memory again.
- Historical 0.3.x → 0.4.0 guide: [migration/0.4.0.md](docs/migration/0.4.0.md).
- The old `MEMORY.md` is only an import source; raw session history stays in `workspace/sessions/*.jsonl`.
- `memory.db` is WAL-mode SQLite; stop the process before copying the workspace, or run `picoclaw memory audit` once to checkpoint an idle store.
- `memory forget` is a hard delete. Run it without `--yes` first to see the cascade preview; `memory replay-forget` re-applies tombstones after restoring a backup.


## Providers and channels

PicoClaw supports OpenAI-compatible endpoints plus Anthropic, Gemini, Bedrock, Azure, and local runtimes. Configure models in `model_list`; the launcher's Models page can add and test them.

Chat channels include Telegram, Discord, Slack, Feishu/Lark, WeCom, WeChat, QQ, DingTalk, LINE, Matrix, IRC, MQTT, VK, and the native Pico WebSocket UI.

- [Provider guide](docs/guides/providers.md)
- [Chat apps guide](docs/guides/chat-apps.md)
- [Configuration reference](docs/guides/configuration.md)

---

## Documentation

- [Online documentation](https://wenyinos.github.io/picoclaw/)
- [Project poster](docs/assets/picoclaw-poster.png)
- [Long-term memory](docs/guides/memory.md) · [中文](docs/guides/memory.zh.md)
- [Migrating to 0.7.x / memory schema v4](docs/reference/memory-schema-v4.md)
- [Historical 0.5.x migration](docs/migration/0.5.10.md)
- [Changelog](CHANGELOG.md)
- [Configuration](docs/guides/configuration.md)
- [Routing and sessions](docs/guides/routing-guide.md)
- [Session system](docs/architecture/session-system.md)
- [Agent evolution](docs/architecture/agent-self-evolution.md)
- [Documentation index](docs/README.md)

---

## About this repository

This repository maintains PicoClaw 0.7.25 with the SQLite memory V4 system (permission domains, independent forget ledger and receipt backends, recovery readiness gate, evidence-bound summaries, owner-only governance, evidence-root cleanup across JSONL and Seahorse, full send-point egress, hybrid retrieval and scene capsules), QQ channel fixes, and OpenCode Go session-header support. Release binaries are published from this repository.

## License

MIT. See [LICENSE](LICENSE).
