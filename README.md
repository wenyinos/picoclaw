<p align="center">
  <img src="assets/logo.webp" alt="PicoClaw" width="360">
</p>

<p align="center">
  <strong>Ultra-lightweight personal AI assistant, written in Go.</strong><br>
  Long-term memory, a companion-oriented dashboard, and a single portable workspace.
</p>

<p align="center">
  <a href="docs/project/README.zh.md">简体中文</a> ·
  <a href="docs/README.md">Documentation</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Version-0.4.0-blue" alt="0.4.0">
  <img src="https://img.shields.io/badge/Arch-x86__64%20%7C%20ARM64%20%7C%20MIPS%20%7C%20RISC--V%20%7C%20LoongArch-blue" alt="Architectures">
  <img src="https://img.shields.io/badge/License-MIT-green" alt="License">
</p>

---

## A personal assistant that remembers

PicoClaw is a small, self-hosted AI assistant that runs on a laptop, a home server, or a $10 board. It connects to the chat apps and model providers you already use, executes tools, and keeps a workspace you own.

**0.4.0 is built around long-term memory and continuity.** Instead of asking the model to rewrite a Markdown file, PicoClaw keeps structured memories in SQLite, deduplicates them, separates real facts from role-play, and injects only what is relevant to the current conversation. The result is an assistant that can maintain a relationship over months without turning its prompt into an ever-growing transcript.

### Memory that lasts

- **SQLite memory store** at `workspace/memory/memory.db`, with exact and near-duplicate detection, conflict superseding, and source tracking.
- **Layered memory**: user profile, relationship state (names, stage, anniversaries, boundaries), episodic events, procedural knowledge, and versioned personas.
- **Real vs. fictional isolation**: role-play and story settings are stored separately and are never mixed into the user's real profile.
- **Human review queue**: uncertain memories wait in `pending_review` and are not injected until approved.
- **Budgeted injection**: fixed profile/relationship blocks plus query-relevant memories, capped by a token budget.
- **Private-channel filtering**: intimate memories are only injected into the channels you mark as private.

### A dashboard for the relationship

The launcher includes a dedicated **Memory** page at `/memory`:

- Overview of entry counts, database size, relationship state, and pending reviews.
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

0.4 also tightens the streaming path: intermediate Pico frames are throttled, the final frame is published before session persistence and compaction, WebSocket writes have deadlines, provider connections are pooled, and session appends are batched. On a local Pico WebSocket test the first visible frame arrives in under a millisecond; model time dominates.

### Runs almost anywhere

- x86_64, ARM64, ARMv7, MIPS, RISC-V, LoongArch.
- Linux, macOS, Windows, Android/Termux, Docker.
- OpenAI-compatible APIs, Anthropic, Gemini, Bedrock, Azure, local models, and more.
- Telegram, Discord, Slack, Feishu, WeCom, QQ, Matrix, IRC, MQTT, and a native Pico WebSocket UI.

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

### Memory CLI

```bash
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render
```

See [Long-term memory](docs/guides/memory.md) for the data model, deduplication rules, review workflow, and API.

---

## Migrating from 0.3.x to 0.4.0

0.4.0 has **no automatic schema migration**. It treats `workspace/memory/memory.db` as the source of truth and no longer reads the old `MEMORY.md` at runtime. Follow these steps once per workspace:

1. **Stop the services**

   ```bash
   systemctl --user stop picoclaw picoclaw-launcher
   ```

2. **Back up the old workspace**

   ```bash
   cp -a ~/.picoclaw/workspace ~/.picoclaw/workspace.backup-$(date +%F)
   ```

3. **Choose how to bring memory over**

   - Keep the existing workspace and import its history:

     ```bash
     picoclaw memory import --from ~/.picoclaw/workspace
     ```

   - Or replace the workspace with a prepared 0.4 workspace (for example the portable archive produced by a previous migration):

     ```bash
     rm -rf ~/.picoclaw/workspace
     tar -xzf picoclaw-workspace-0.4-portable.tar.gz -C ~/.picoclaw
     chown -R <service-user>:<service-group> ~/.picoclaw/workspace
     ```

4. **Generate the readable view and check the result**

   ```bash
   picoclaw memory render
   picoclaw memory audit
   ```

   `memory import` converts old `MEMORY.md` bullets into `pending_review` profile candidates and folds repetitive legacy task records.

5. **Use a portable workspace path**

   ```json
   "agents": { "defaults": { "workspace": "workspace" } }
   ```

   A relative value is resolved against `PICOCLAW_HOME`; absolute paths still work.

6. **Start and review**

   ```bash
   systemctl --user start picoclaw picoclaw-launcher
   ```

   Open `http://localhost:18800/memory` and process the review queue. Confirm real facts, mark role-play as fictional, and archive expired events.

Notes:

- The old `MEMORY.md` is not read by 0.4. Import it before deleting the old workspace if you want to keep its facts.
- Raw session history remains in `workspace/sessions/*.jsonl` and is unchanged.
- `memory.db` is WAL-mode SQLite; stop the process before copying the workspace, or run `picoclaw memory audit` once to checkpoint an idle store.

---

## Providers and channels

PicoClaw supports OpenAI-compatible endpoints plus Anthropic, Gemini, Bedrock, Azure, and local runtimes. Configure models in `model_list`; the launcher's Models page can add and test them.

Chat channels include Telegram, Discord, Slack, Feishu/Lark, WeCom, WeChat, QQ, DingTalk, LINE, Matrix, IRC, MQTT, VK, and the native Pico WebSocket UI.

- [Provider guide](docs/guides/providers.md)
- [Chat apps guide](docs/guides/chat-apps.md)
- [Configuration reference](docs/guides/configuration.md)

---

## Documentation

- [Long-term memory](docs/guides/memory.md) · [中文](docs/guides/memory.zh.md)
- [Migrating from 0.3.x to 0.4.0](docs/migration/0.4.0.md)
- [Changelog](CHANGELOG.md)
- [Configuration](docs/guides/configuration.md)
- [Routing and sessions](docs/guides/routing-guide.md)
- [Session system](docs/architecture/session-system.md)
- [Agent evolution](docs/architecture/agent-self-evolution.md)
- [Documentation index](docs/README.md)

---

## About this repository

This repository maintains PicoClaw 0.4 with the SQLite memory system, QQ channel fixes, and OpenCode Go session-header support. Release binaries are published from this repository.

## License

MIT. See [LICENSE](LICENSE).
