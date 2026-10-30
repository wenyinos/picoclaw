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

**0.5 is built around long-term memory and continuity.** Instead of asking the model to rewrite a Markdown file, PicoClaw keeps structured memories in SQLite, deduplicates them, separates real facts from role-play, and injects only what is relevant to the current conversation. The result is an assistant that can maintain a relationship over months without turning its prompt into an ever-growing transcript.

### Memory that lasts

- **SQLite memory store** at `workspace/memory/memory.db`, with exact and near-duplicate detection, conflict superseding, and source tracking.
- **Layered memory**: user profile, relationship state (names, stage, anniversaries, boundaries), episodic events, procedural knowledge, and versioned personas.
- **Real vs. fictional isolation**: role-play and story settings are stored separately and are never mixed into the user's real profile.
- **Human review queue**: uncertain memories wait in `pending_review` and are not injected until approved.
- **Budgeted injection**: fixed profile/relationship blocks plus query-relevant memories, capped by a token budget; boundary rules are appended outside that budget so they cannot be evicted.
- **Schema v2 migration**: one-click upgrade from `/memory` with a `VACUUM INTO` backup; facets keep different sides of the same predicate from overwriting each other.
- **Relationship retrieval**: relationship notes can be retrieved alongside events and procedures, capped per turn so they do not crowd out other memories.
- **Private-channel filtering**: intimate memories are only injected into the channels you mark as private.

### A dashboard for the relationship

The launcher includes a dedicated **Memory** page at `/memory`:

- Overview of entry counts, database size, relationship state, and pending reviews.
- Database migration: schema version, empty predicates, relationship count, one-click backup and upgrade.
- Search and edit memories by realm, layer, and status.
- Review queue to confirm real facts, mark fiction, or archive noise.
- Relationship and persona editor with version history.
- Settings for budget, auto-extraction, private channels, and retention.
- One-shot import of a 0.3.x workspace.

### Workspace you can move anywhere

- The workspace contains no machine-specific absolute paths.
- `agents.defaults.workspace` accepts a relative value such as `"workspace"`, resolved against `PICOCLAW_HOME`.
- `memory.db` is self-contained; stop the process, copy the directory, and start it on another machine.
- Sessions are append-only JSONL and are written asynchronously, so saving history stays off the response path.

### Faster, quieter responses

0.5 also tightens the streaming path: intermediate Pico frames are throttled, the final frame is published before session persistence and compaction, WebSocket writes have deadlines, provider connections are pooled, and session appends are batched. On a local Pico WebSocket test the first visible frame arrives in under a millisecond; model time dominates.

### Runs almost anywhere

- x86_64, ARM64, ARMv7, RISC-V, LoongArch.
- Linux/mipsle is not published in 0.5: the pure-Go SQLite driver does not support MIPS32.
- Linux, macOS, Windows, Android/Termux, Docker.
- OpenAI-compatible APIs, Anthropic, Gemini, Bedrock, Azure, local models, and more.
- Telegram, Discord, Slack, Feishu, WeCom, QQ, Matrix, IRC, MQTT, and a native Pico WebSocket UI.

---

## Core capabilities

The 0.5 memory work sits alongside PicoClaw's original assistant features:

- **Chat channels** — Telegram, Discord, Slack, Feishu/Lark, WeCom, WeChat, QQ, DingTalk, LINE, Matrix, IRC, MQTT, VK, and the native Pico WebSocket UI. See [Chat apps](docs/guides/chat-apps.md).
- **Model providers** — OpenAI-compatible APIs, Anthropic, Gemini, Bedrock, Azure, OAuth providers, and local runtimes; fallback chains and a light model for side questions. See [Providers](docs/guides/providers.md).
- **Tools** — file read/write/edit, shell execution, web search/fetch, messaging, media and image handling, scheduling, and sub-agents. See [Tools](docs/reference/tools_configuration.md).
- **MCP** — connect external Model Context Protocol servers and expose their tools to the agent. See [MCP CLI](docs/reference/mcp-cli.md).
- **Skills** — workspace, global, and builtin skill sources; search, install, and use skills from chat. See [Configuration](docs/guides/configuration.md).
- **Automation** — cron jobs with live CLI reload, direct delivery, shell commands, and visible failure state; heartbeat tasks, hooks, and optional agent self-evolution. See [Cron](docs/reference/cron.md), [Hooks](docs/architecture/hooks/README.md), and [Agent evolution](docs/architecture/agent-self-evolution.md).
- **Sub-agents** — async background work and long-running task orchestration. See [Spawn and async tasks](docs/guides/spawn-tasks.md).
- **Interfaces** — CLI (`onboard`, `agent`, `gateway`), desktop launcher, Docker, and Android/Termux. See [Docker](docs/guides/docker.md) and [Termux](docs/guides/android-termux.md).
- **Memory** — SQLite long-term memory, relationship/persona state, review queue, cross-layer search, repair dashboard, and CLI. See [Long-term memory](docs/guides/memory.md).

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
    "private_channels": ["pico"],
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
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render
```

See [Long-term memory](docs/guides/memory.md) for the data model, deduplication rules, review workflow, and API.

---

## Migrating from 0.3.x / 0.4.x to 0.5.x

0.5 keeps `workspace/memory/memory.db` as the source of truth and adds schema v2. Existing 0.4.x databases are upgraded in place; there is no manual SQL step. On first open, 0.5.x creates a `VACUUM INTO` backup and upgrades the schema. The data migration runs from `/memory` → “Database migration”, or with `picoclaw memory migrate`.

1. **Stop the services**

   ```bash
   systemctl --user stop picoclaw picoclaw-launcher
   ```

2. **Back up the workspace**

   ```bash
   cp -a ~/.picoclaw/workspace ~/.picoclaw/workspace.backup-$(date +%F)
   ```

3. **Start 0.5.x and open the migration page**

   ```bash
   systemctl --user start picoclaw picoclaw-launcher
   ```

   Open `http://localhost:18800/memory` → “Database migration” and run the migration. The CLI equivalent is:

   ```bash
   picoclaw memory migrate
   ```

   The migration is idempotent. It upgrades the schema, turns empty predicates into `待分类`, maps the reference v13–v21 relationship entries to predicate/facet, and writes a new `memory.db.backup-*` file each time.

4. **Verify**

   ```bash
   sqlite3 ~/.picoclaw/workspace/memory/memory.db "PRAGMA integrity_check;"
   ```

   Also check the entry counts and that a public channel cannot see a `fictional/private` persona.

5. **0.3.x workspaces only**

   Run `picoclaw memory import --from ~/.picoclaw/workspace` once before or after the upgrade. The old `MEMORY.md` is never read at runtime.

Notes:

- The v0.4.6 tag remains available; install 0.5.x to get schema v2 and the Launcher migration page.
- Historical 0.3.x → 0.4.0 guide: [migration/0.4.0.md](docs/migration/0.4.0.md).
- The old `MEMORY.md` is only an import source; raw session history stays in `workspace/sessions/*.jsonl`.
- `memory.db` is WAL-mode SQLite; stop the process before copying the workspace, or run `picoclaw memory audit` once to checkpoint an idle store.


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
- [Migrating from 0.3.x / 0.4.x to 0.5.x](docs/migration/0.5.0.md)
- [Changelog](CHANGELOG.md)
- [Configuration](docs/guides/configuration.md)
- [Routing and sessions](docs/guides/routing-guide.md)
- [Session system](docs/architecture/session-system.md)
- [Agent evolution](docs/architecture/agent-self-evolution.md)
- [Documentation index](docs/README.md)

---

## About this repository

This repository maintains PicoClaw 0.5.1 with the SQLite memory system, QQ channel fixes, and OpenCode Go session-header support. Release binaries are published from this repository.

## License

MIT. See [LICENSE](LICENSE).
