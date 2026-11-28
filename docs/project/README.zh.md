<p align="center">
  <img src="../assets/logo.webp" alt="PicoClaw" width="360">
</p>

<p align="center">
  <strong>用 Go 编写的超轻量个人 AI 助手。</strong><br>
  长期记忆、面向陪伴场景的控制台，以及一个可以整体复制迁移的 workspace。
</p>

<p align="center">
  <a href="https://github.com/wenyinos/picoclaw/blob/main/README.md">English</a> ·
  <a href="https://wenyinos.github.io/picoclaw/">在线文档</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/github/v/release/wenyinos/picoclaw?label=Version&color=blue" alt="版本">
  <img src="https://img.shields.io/badge/Arch-x86__64%20%7C%20ARM64%20%7C%20RISC--V%20%7C%20LoongArch-blue" alt="架构">
  <img src="https://img.shields.io/badge/License-MIT-green" alt="许可证">
</p>

---

## 一个会记住你的个人助手

PicoClaw 是一个可以运行在笔记本、家庭服务器或十美元开发板上的自托管 AI 助手。它接入你现有的聊天应用和模型服务，能执行工具，并把数据保存在你自己的 workspace 里。

**0.7.25 是当前稳定版本。** 0.7 系列把记忆 V4 从基础结构推进到可治理、故障关闭的闭环：归属域、带备份的独立遗忘账本、证据约束摘要、分后端持久清理回执，以及覆盖主调用、fallback、`/btw`、子代理和后台摘要的发送点门禁。不再让模型反复改写 Markdown 文件，而是把记忆以结构化条目存进 SQLite，自动去重、处理冲突、区分真实事实与角色扮演，并且只把当前对话相关的部分注入提示词。

### 0.7 进度

| 版本 | 状态 | 主要内容 |
|---|---|---|
| 0.7.0 | 已发布 | Schema v4、域/绑定/许可、独立遗忘账本、来源事件、摘要证据、承诺/回忆、持久任务 |
| 0.7.1 | 已发布 | fallback/流式发送点门禁基础、JSONL 遗忘清理、治理工作台 |
| 0.7.2 | 已发布 | 显式迁移；v3/缺对象 v4 数据库故障关闭 |
| 0.7.3 | 已发布 | 私密渠道排除制；群聊/公开会话不参与自动抽取 |
| 0.7.4 | 已发布 | 恢复 deprecated `private_channels`，修复 0.7.3 启动阻断 |
| 0.7.10 | 已发布 | chat kind 贯穿提示词/工具、工具统一治理、存储层秘密禁入、单 Provider 发送门禁、账本备份/校验、FTS/LIKE 权限前置、Correct/摘要/清理一致性 |
| 0.7.20 | 已发布 | Correct 墓碑强制、模型治理收口到 owner、严格写入 DTO、审计 actor/request、原子新库初始化与修复前备份、Seahorse 会话隔离和逐条清理、来源根证据、全发送点门禁、TTL/层过滤前置 |
| 0.7.25 | 当前稳定版 | Replay blocked/revision 门禁、恢复 readiness、分 locator 账本回执、后继清理任务、origin event 生产接线、持久抽取引用、realm/导入隔离、Seahorse race 修复、预算/LIKE/egress 修正、OpenAPI 3.1 |

v4 之后没有主库 Schema 变更；0.7.25 增加账本内部回执表与恢复门禁。剩余边界已明确：完整持久 `MemoryUseManifest`、临时无记忆会话、外部 LLM 抽取、embedding/向量检索、MMR、主动投递调度、完整多域 relationship、长文分块与时间抽取尚未接线。

### 能长期保存的记忆

- **SQLite 记忆库**：位于 `workspace/memory/memory.db`，支持精确/近似去重、冲突替代，并为每次写入保留证据链。
- **分层记忆**：用户画像、关系状态（称呼、阶段、纪念日、边界）、事件记忆、流程知识和版本化人设。
- **真实与虚构隔离**：角色扮演和故事设定单独保存，不会混入真实用户画像；写入防火墙会拦住台词和设定稿。
- **人工审核队列**：不确定的记忆进入 `pending_review`，审核通过前不注入提示词。
- **独立遗忘账本**：`forget` 先写 `forget-ledger.db`，再修改主库；恢复旧备份不会复活已遗忘内容，`replay-forget` 按 manifest entry ID 与权限域重放，不再全库扫描正文哈希。账本缺失或身份损坏时读取仍可用，但 `forget` 故障关闭；迁移/修复备份同时保存主库与账本；`memory forget-audit` 可只读审计历史操作。
- **权限域与 chat kind**：条目带 `domain_id`，去重、自动替换和胶囊读取不跨域；群聊/公开会话在提示词与 `memory` 工具中只允许 audience 为空或 `all`。
- **工具统一治理**：模型仅保留 `add / search / inspect / 非核心 correct / legacy capsule / forget 预览` 白名单，全部经 `MemoryService` 携带可信 domain、channel 与 chat kind；确认、归档/恢复、关系状态以及任何 force/pinned/boundary 变更只能由 owner 执行；密码、验证码、API Key、私钥在 `Store.Add` 层直接拒绝。
- **证据约束摘要**：手工主题/阶段簇、逐 claim 绑定来源、发布前校验 generation 与 cluster 成员、变更即失效。摘要是可撤销视图，不是权威事实。
- **时间语义**：今天/昨天/上周/去年夏天等短语被确定性地解析为 `date / range / approximate / planned`；近似时间不伪造单日。
- **混合检索**：Anchor（facet/predicate/标题）、FTS5、LIKE、时间重叠和 1-Hop 关系扩展；FTS/LIKE 的权限过滤也进入 SQL `LIMIT` 之前，不可见记录不会挤掉合法命中。
- **场景胶囊**：`load_capsule` 一次取回 T0 写作铁律（完整版或紧凑版），按真实 chat kind/渠道隔离，公开或群聊整体返回空。
- **真实预算记账**：固定块、边界块、检索块分别计数，`memory audit` 打印实测 token。
- **Schema v4 迁移**：仅 v3 → v4 单段显式迁移，先 `VACUUM INTO` 备份主库与账本；v1/v2 必须先用 0.6.x 升到 v3。
- **发送点门禁**：`memory.allowed_chat_recipients` 非空时，携带记忆或会话派生内容的主调用、fallback、重试、流式、`/btw`、子代理与后台摘要都必须匹配，否则返回不可重试的 `memory egress denied`；策略拒绝后不会静默改投。
- **跨存储遗忘清理**：JSONL 与 Seahorse 都先验证完整来源根再删除，覆盖重复消息、工具调用文本和依赖被删消息的摘要；无法核验的片段保持 `partial/unavailable`，不猜测删除。
- **渠道可见性（排除制）**：默认所有渠道可读私密记忆，在设置页排除个别渠道；群聊/公开会话在提示词和工具中始终不可见。

### 面向关系的控制台

Launcher 新增独立的 **记忆** 页面（`/memory`）：

- 概览：条目数量、数据库大小、关系状态、待审核数量。
- 数据库迁移：schema 版本、空 predicate、relationship 数量、FTS 行数与 tokenizer，一键备份升级。
- 按领域、类型、状态搜索和编辑记忆。
- 审核队列：确认真实事实、标记虚构、归档噪声。
- 关系与人格编辑：称呼、阶段、纪念日、边界、人设版本。
- 设置：注入预算、自动抽取、私密渠道、保留周期。
- 一次性导入 0.3.x workspace。

### workspace 可以随意迁移

- workspace 内部不保存机器相关的绝对路径。
- `agents.defaults.workspace` 支持相对路径 `"workspace"`，运行时按 `PICOCLAW_HOME` 解析。
- `memory.db` 与 `forget-ledger.db` 自包含；停止进程后复制整个 memory 目录即可换机器运行，恢复时两者必须成对使用。
- 会话使用追加式 JSONL，并异步落盘，不阻塞消息返回。

### 更快、更安静的回答

0.5 的流式优化仍然保留：Pico 中间帧限流，最终帧在会话保存和压缩之前发布，WebSocket 写操作有超时，模型连接池复用，会话追加批量落盘。本机 Pico WebSocket 测试中首个可见帧在 1ms 内到达，主要延迟来自模型本身。

### 几乎随处可跑

- x86_64、ARM64、ARMv7、RISC-V、LoongArch。
- 不发布 Linux/mipsle：纯 Go SQLite 驱动不支持 MIPS32。
- Linux、macOS、Windows、Android/Termux、Docker。
- OpenAI 兼容接口、Anthropic、Gemini、Bedrock、Azure、本地模型等。
- Telegram、Discord、Slack、飞书、企业微信、QQ、Matrix、IRC、MQTT，以及原生 Pico WebSocket 界面。

---

## 核心能力

0.7 的记忆能力建立在 PicoClaw 原有功能之上：

- **聊天渠道** — Telegram、Discord、Slack、飞书/Lark、企业微信、微信、QQ、钉钉、LINE、Matrix、IRC、MQTT、VK，以及原生 Pico WebSocket 界面。见[聊天应用](../guides/chat-apps.zh.md)。
- **模型服务** — OpenAI 兼容接口、Anthropic、Gemini、Bedrock、Azure、OAuth 服务和本地模型；支持 fallback 链和侧问题轻量模型。见[模型配置](../guides/providers.zh.md)。
- **工具** — 文件读写编辑、Shell 执行、网页搜索/抓取、消息发送、媒体与图片处理、定时任务和子代理。见[工具配置](../reference/tools_configuration.zh.md)。
- **MCP** — 连接外部 Model Context Protocol 服务，把其工具暴露给 Agent。见 [MCP CLI](../reference/mcp-cli.md)。
- **技能** — 工作区、全局和内置技能源；支持搜索、安装并在对话中使用。见[配置指南](../guides/configuration.zh.md)。
- **自动化** — 定时任务支持 CLI 热加载、直接投递、Shell 命令和失败状态可见；另有心跳任务、Hooks 和可选的 Agent 自进化。见[定时任务](../reference/cron.md)、[Hooks](../architecture/hooks/README.zh.md) 和 [Agent 自进化](../architecture/agent-self-evolution.md)。
- **子代理** — 异步后台任务和长任务编排。见[派生与异步任务](../guides/spawn-tasks.md)。
- **使用界面** — CLI（`onboard`、`agent`、`gateway`）、桌面 Launcher、Docker 和 Android/Termux。见 [Docker](../guides/docker.zh.md) 和 [Termux](../guides/android-termux.md)。
- **记忆** — SQLite 长期记忆、关系/人设、审核队列、跨层搜索、数据库修复、控制台和 CLI。见[长期记忆](../guides/memory.zh.md)。

---

## 快速开始

### 使用发布二进制

```bash
picoclaw onboard
picoclaw gateway
# 可选桌面控制台
picoclaw-launcher
```

打开 `http://localhost:18800` 使用 Launcher，记忆页面位于 `/memory`。

### 从源码构建

需要 Go 1.25+、Node.js 20+、pnpm、make。

```bash
git clone https://github.com/wenyinos/picoclaw.git
cd picoclaw

make build              # 终端/gateway 二进制
make build-launcher     # 内嵌 Web UI 的桌面 Launcher
```

产物输出到 `build/`。

如果直接调用 Go，请带上项目构建标签：

```bash
CGO_ENABLED=0 go build -tags goolm,stdjson ./...
```

`goolm` 选择 mautrix 的纯 Go 加密实现。缺少它时会走默认的 libolm 后端，需要 CGO 和系统 libolm，因此 `CGO_ENABLED=0` 构建会因为 `crypto/libolm` 包没有可编译文件而失败。

---

## 记忆配置

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

0.3.x 的旧配置不需要手动添加 `memory` 节：加载时会自动补齐默认值。把 workspace 写成相对路径 `"workspace"`，配置即可跨机器、跨用户迁移。

SQLite 数据库是唯一的运行时记忆源。PicoClaw 运行时永远不会读取 `MEMORY.md` 或每日笔记；即使 `memory.enabled` 为 `false`，也只是完全不注入长期记忆，不会回退到 Markdown。

### 记忆 CLI

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

数据模型、去重规则、审核流程和 API 详见 [长期记忆指南](../guides/memory.zh.md)。

---

## 迁移到 0.7.25

0.7 仍以 `workspace/memory/memory.db` 为唯一事实源，并把 schema 从 v3 单段升级到 v4。**0.7.2 起不再在启动时自动迁移**：已有 v3 数据库（或缺对象的 v4 库）以故障关闭模式打开，普通读写返回 `schema migration required`，直到运行 `picoclaw memory migrate` 或使用 `/memory` → “数据库迁移”。v1 / v2 数据库不会被就地升级：先用 0.6.x 打开一次升到 v3。完整步骤见 [Memory Schema v4](../reference/memory-schema-v4.zh.md)。

1. **停止服务**

   ```bash
   systemctl --user stop picoclaw picoclaw-launcher
   ```

2. **备份 workspace**

   ```bash
   cp -a ~/.picoclaw/workspace ~/.picoclaw/workspace.backup-$(date +%F)
   ```

3. **启动 0.7.25 并打开迁移页**

   ```bash
   systemctl --user start picoclaw picoclaw-launcher
   ```

   打开 `http://localhost:18800/memory` → “数据库迁移”，执行迁移。命令行等价操作：

   ```bash
   picoclaw memory migrate
   ```

   迁移是幂等的：新增 v4 治理列与表、逐字段重建 `sources`、建立隔离域 `legacy_unassigned`、把精确指纹索引改为域内唯一、缺失时重建 FTS，并在 `memory/backups/` 轮转生成 `memory.db.bak-*` 与 `forget-ledger.db.bak-*` 快照（各保留最新 2 份）。已删除的 v1 → v2 → v3 修复不会重放。

4. **验证**

   ```bash
   sqlite3 ~/.picoclaw/workspace/memory/memory.db "PRAGMA integrity_check;"
   ```

   同时检查条目数，以及公开渠道是否看不到 `fictional/private` 伴侣人设。

5. **仅 0.3.x workspace**

   在升级前后运行一次 `picoclaw memory import --from ~/.picoclaw/workspace`。旧 `MEMORY.md` 运行时永远不会被读取。

说明：

- v0.4.6 tag 仍然保留；安装 0.7.25 才能获得 schema v4、故障关闭的遗忘账本、分 locator 清理回执、恢复门禁和 Launcher 迁移页。
- 恢复数据库备份后，先用 `picoclaw memory ledger-verify --backup <账本快照>` 校验主库与账本身份，再重放遗忘；重新启用长期记忆前运行 `picoclaw memory recovery-status`。
- 历史 0.3.x → 0.4.0 迁移文档：[migration/0.4.0.zh.md](../migration/0.4.0.zh.md)。
- 旧 `MEMORY.md` 只作为导入源；原始会话历史仍在 `workspace/sessions/*.jsonl`。
- `memory.db` 是 WAL 模式 SQLite；复制 workspace 前先停进程，或运行一次 `picoclaw memory audit` checkpoint。


## 模型与渠道

PicoClaw 支持 OpenAI 兼容接口，以及 Anthropic、Gemini、Bedrock、Azure 和本地模型。在 `model_list` 中配置模型，Launcher 的模型页面可以添加和测试。

聊天渠道包括 Telegram、Discord、Slack、飞书/Lark、企业微信、微信、QQ、钉钉、LINE、Matrix、IRC、MQTT、VK，以及原生 Pico WebSocket 界面。

- [模型配置指南](../guides/providers.zh.md)
- [聊天应用配置](../guides/chat-apps.zh.md)
- [配置参考](../guides/configuration.zh.md)

---

## 文档

- [在线文档](https://wenyinos.github.io/picoclaw/)
- [项目海报](../assets/picoclaw-poster.png)
- [长期记忆](../guides/memory.zh.md) · [English](../guides/memory.md)
- [迁移到 0.7.x / Schema v4](../reference/memory-schema-v4.zh.md)
- [历史 0.5.x 迁移](../migration/0.5.10.zh.md)
- [变更日志](https://github.com/wenyinos/picoclaw/blob/main/CHANGELOG.md)
- [配置指南](../guides/configuration.zh.md)
- [路由与会话](../guides/routing-guide.md)
- [会话系统](../architecture/session-system.zh.md)
- [Agent 自我进化](../architecture/agent-self-evolution.md)
- [文档索引](../README.md)

---

## 关于本仓库

本仓库维护 PicoClaw 0.7.25，包含 SQLite 记忆 V4（权限域、独立遗忘账本与分后端回执、恢复门禁、证据约束摘要、owner-only 治理、JSONL/Seahorse 来源根清理、完整发送点门禁、混合检索与场景胶囊）、QQ 通道修复和 OpenCode Go session header 支持。发布二进制由本仓库构建。

## 许可证

MIT，详见 [LICENSE](https://github.com/wenyinos/picoclaw/blob/main/LICENSE)。
