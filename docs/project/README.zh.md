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

**0.4 围绕长期记忆与关系连续性设计。** 不再让模型反复改写一个 Markdown 文件，而是把记忆以结构化条目存进 SQLite，自动去重、处理冲突、区分真实事实与角色扮演，并且只把当前对话相关的部分注入提示词。这样助手可以维持数月的关系与上下文，而不会让提示词变成越来越长的流水账。

### 能长期保存的记忆

- **SQLite 记忆库**：位于 `workspace/memory/memory.db`，支持精确/近似去重、冲突替代和来源追踪。
- **分层记忆**：用户画像、关系状态（称呼、阶段、纪念日、边界）、事件记忆、流程知识和版本化人设。
- **真实与虚构隔离**：角色扮演和故事设定单独保存，不会混入真实用户画像。
- **人工审核队列**：不确定的记忆进入 `pending_review`，审核通过前不注入提示词。
- **预算注入**：固定注入画像/关系/边界，再按当前消息检索相关记忆，总量受 token 预算限制。
- **私密渠道过滤**：亲密记忆只注入到你标记为私密的渠道。

### 面向关系的控制台

Launcher 新增独立的 **记忆** 页面（`/memory`）：

- 概览：条目数量、数据库大小、关系状态、待审核数量。
- 按领域、类型、状态搜索和编辑记忆。
- 审核队列：确认真实事实、标记虚构、归档噪声。
- 关系与人格编辑：称呼、阶段、纪念日、边界、人设版本。
- 设置：注入预算、自动抽取、私密渠道、保留周期。
- 一次性导入 0.3.x workspace。

### workspace 可以随意迁移

- workspace 内部不保存机器相关的绝对路径。
- `agents.defaults.workspace` 支持相对路径 `"workspace"`，运行时按 `PICOCLAW_HOME` 解析。
- `memory.db` 自包含；停止进程后复制整个目录即可换机器运行。
- 会话使用追加式 JSONL，并异步落盘，不阻塞消息返回。

### 更快、更安静的回答

0.4 同时收紧了流式链路：Pico 中间帧限流，最终帧在会话保存和压缩之前发布，WebSocket 写操作有超时，模型连接池复用，会话追加批量落盘。本机 Pico WebSocket 测试中首个可见帧在 1ms 内到达，主要延迟来自模型本身。

### 几乎随处可跑

- x86_64、ARM64、ARMv7、RISC-V、LoongArch。
- 0.4 不再发布 Linux/mipsle：纯 Go SQLite 驱动不支持 MIPS32。
- Linux、macOS、Windows、Android/Termux、Docker。
- OpenAI 兼容接口、Anthropic、Gemini、Bedrock、Azure、本地模型等。
- Telegram、Discord、Slack、飞书、企业微信、QQ、Matrix、IRC、MQTT，以及原生 Pico WebSocket 界面。

---

## 核心能力

0.4 的记忆能力建立在 PicoClaw 原有功能之上：

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
    "private_channels": ["pico"],
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
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render
```

数据模型、去重规则、审核流程和 API 详见 [长期记忆指南](../guides/memory.zh.md)。

---

## 从 0.3.x 迁移到 0.4.0

0.4.0 **没有自动 schema 迁移**。它只把 `workspace/memory/memory.db` 作为事实源，运行时不再读取旧的 `MEMORY.md`。每个 workspace 执行一次以下步骤：

1. **停止服务**

   ```bash
   systemctl --user stop picoclaw picoclaw-launcher
   ```

2. **备份旧 workspace**

   ```bash
   cp -a ~/.picoclaw/workspace ~/.picoclaw/workspace.backup-$(date +%F)
   ```

3. **选择迁移方式**

   - 保留现有 workspace，导入历史数据：

     ```bash
     picoclaw memory import --from ~/.picoclaw/workspace
     ```

   - 或直接替换为准备好的 0.4 workspace（例如之前生成的便携归档）：

     ```bash
     rm -rf ~/.picoclaw/workspace
     tar -xzf picoclaw-workspace-0.4-portable.tar.gz -C ~/.picoclaw
     chown -R <service-user>:<service-group> ~/.picoclaw/workspace
     ```

4. **可选：生成可读导出并检查**

   ```bash
   picoclaw memory render   # 可选 Markdown 导出，不会被运行时读回
   picoclaw memory audit
   ```

   `memory import` 会把旧 `MEMORY.md` 的条目转成 `pending_review` 用户画像候选，并折叠重复的历史任务记录。

5. **使用可迁移的 workspace 路径**

   ```json
   "agents": { "defaults": { "workspace": "workspace" } }
   ```

   相对路径按 `PICOCLAW_HOME` 解析；绝对路径仍然可用。

6. **启动并审核**

   ```bash
   systemctl --user start picoclaw picoclaw-launcher
   ```

   打开 `http://localhost:18800/memory`，处理审核队列：确认真实事实、把角色扮演标记为虚构、归档过期事件。

注意：

- 运行时永远不会读取旧 `MEMORY.md`，即使记忆功能关闭也不会回退。如果希望保留其中的事实，请在删除旧 workspace 前执行导入。
- 原始会话历史仍在 `workspace/sessions/*.jsonl`，格式不变。
- `memory.db` 使用 WAL 模式；复制 workspace 前请停止进程，或先执行一次 `picoclaw memory audit` 让空闲库完成 checkpoint。

---

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
- [从 0.3.x 迁移到 0.4.0](../migration/0.4.0.zh.md)
- [变更日志](https://github.com/wenyinos/picoclaw/blob/main/CHANGELOG.md)
- [配置指南](../guides/configuration.zh.md)
- [路由与会话](../guides/routing-guide.md)
- [会话系统](../architecture/session-system.zh.md)
- [Agent 自我进化](../architecture/agent-self-evolution.md)
- [文档索引](../README.md)

---

## 关于本仓库

本仓库维护 PicoClaw 0.4，包含 SQLite 记忆系统、QQ 通道修复和 OpenCode Go session header 支持。发布二进制由本仓库构建。

## 许可证

MIT，详见 [LICENSE](https://github.com/wenyinos/picoclaw/blob/main/LICENSE)。
