# 长期记忆（SQLite）

PicoClaw 使用 SQLite 保存长期记忆，替代旧的 `MEMORY.md` 自由文本模式。记忆库是唯一运行时事实源。`MEMORY.md` 只是可选的手动导出：运行时永远不会读取，即使 `memory.enabled` 为 `false` 也不会回退。

## 可移植性

- workspace 内部不保存本机绝对路径；记忆库、索引、导入报告和会话元数据都可随目录复制。
- 配置里的 `agents.defaults.workspace` 可以写成相对路径 `"workspace"`，运行时按 `PICOCLAW_HOME` 解析。
- 复制前停止进程并确保 WAL 已 checkpoint；只需保留 `memory/memory.db`。
- 复制后按运行服务的用户执行 `chown -R <user>:<group> workspace`。

## 目录

```text
workspace/
├── memory/
│   ├── memory.db          # SQLite 主库（WAL）
│   ├── MEMORY.md          # 可选导出，运行时不会读取
│   └── import-report.md   # 一次性导入报告
└── sessions/              # 原始会话历史，不变
```

## 配置

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

- `enabled`：为 `false` 时完全关闭长期记忆，不会回退到 Markdown。
- `budget_tokens`：固定块与检索块的总注入预算；`boundary` 在固定块预算之外单独拼接，不参与裁剪，但仍受 audience / 私有渠道 / fictional 过滤。
- `auto_extract`：每轮结束后用规则抽取候选记忆。
- `review_required`：新抽取的未知领域记忆进入待审核队列，不进入提示词。
- `private_channels`：`audience=private` 的记忆只在这些渠道注入。
- `inject_fictional`：是否把角色扮演/虚构记忆注入真实对话，默认关闭。

## 记忆分类

| 字段 | 取值 | 说明 |
|---|---|---|
| `realm` | real / fictional / work / unknown | 真实用户事实 / 角色扮演 / 工作流程 / 待审核 |
| `layer` | persona / profile / relationship / episodic / procedural / boundary | 人设 / 画像 / 关系 / 事件 / 流程 / 边界 |
| `status` | active / pending_review / superseded / archived | 生命周期 |

`relationship_state` 保存称呼、关系阶段、亲密度、纪念日等当前状态；`personas` 保存版本化人设。`predicate` 使用受控词表，`facet` 区分同一 predicate 下的不同侧面；只有 profile 的单值事实或显式 `supersedes_id` 会自动覆盖旧值。`boundary` 规则不参与固定块预算裁剪：它们作为安全块单独拼接，避免被过长的 profile / relationship 挤掉。

## 自动抽取

每轮结束后只处理明确的用户陈述：`记住/记一下/别忘了`、称呼、偏好/过敏、生日/纪念日和带日期的事件。普通聊天、角色扮演关键词（`设定/扮演/剧情/人设/世界观/虚构/故事线`）以及助手回复都不会被自动保存。

## 去重与冲突

写入时按顺序执行：

1. 归一化 + `sha256` 精确去重。
2. 已知模板折叠（例如旧版“判断是否值得长期记住”的重复任务）。
3. 中文 bigram Jaccard ≥ 0.75 或 SimHash Hamming ≤ 3 近似合并。
4. 同 `realm+subject+predicate` 不同值触发 supersede，旧值保留为历史。
5. `unknown` 默认进入待审核。

## 搜索语义

`memory` 工具的 search 面向管理检索：默认查全部 layer，支持 `realm` / `layer` / `predicate` / `facet` / `status` 过滤，并且可以看到 private 记忆。提示词注入使用更窄的检索路径：只取 active 的 relationship / episodic / procedural，按相关性过滤，并继续执行 audience / 私有渠道 / fictional 隔离。relationship 条目单次最多注入 2 条，避免长关系设定挤掉事件和流程。固定块本体仍按预算裁剪；`boundary` 在固定块之后单独拼接，不参与该裁剪。

## 数据库迁移（schema v2）

新版本会把 `memory.db` 升级到 schema v2：

- `entries` 增加 `facet`，同一 `subject+predicate` 下的不同侧面可以共存；
- `personas` 支持 `(id, version)` 多版本，并增加 `realm` / `audience` / `scope`，伴侣人设可以只进入私有渠道；
- `relationship_state` 增加 `expires_at`，并用 `relationship_state_history` 保留旧值；
- 空 `predicate` 统一标记为“待分类”，不再产生新的空键；
- 参考库中 v13–v21 的 9 条 relationship 会写入 `身份关系 / 权力结构 / 心理状态 / 事件·第一次` 及对应 facet，保持 active。

迁移会先执行 SQLite `VACUUM INTO` 备份，只改分类，不删除条目。可以在 `/memory` 的“数据库迁移”页一键执行，也可以运行：

```bash
picoclaw memory migrate
```

## 后台页面

启动 launcher 后打开 `/memory`：

- 概览：条目统计、数据库大小、关系状态、归档操作。
- 数据库迁移：查看 schema 版本、空 predicate、relationship 数量，并一键备份迁移。
- 记忆库：服务端分页（每页 100 条）、搜索、筛选、编辑、归档、恢复。
- 审核：确认真实记忆 / 标记虚构 / 标记工作 / 忽略。
- 关系与人格：称呼、阶段、纪念日、边界、人设版本；人设可设置 realm / audience / scope。
- 设置：预算、自动抽取、私有渠道、导入旧 workspace。
- 数据库修复：按 realm / layer / status / audience / 关键词预览并批量迁移；执行前自动用 SQLite `VACUUM INTO` 备份。

## CLI

```bash
picoclaw memory migrate
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render   # 可选 Markdown 导出，不会被运行时读回
```

`memory import` 是唯一会读取旧 `MEMORY.md` 的命令；运行时不读取。

## 从 0.3.x 迁移

0.3.x → 0.4.x 不做运行时迁移。新版本只读取 `memory.db`：

1. 停止 gateway 与 launcher。
2. 备份旧 `workspace/memory`。
3. 使用新 workspace，并运行一次 `memory import` 导入旧任务记录和摘要；也可以在替换前导入。
4. 启动后在 `/memory` 审核待确认条目。

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
POST   /api/memory/import
GET    /api/memory/export
GET    /api/memory/stats
```
