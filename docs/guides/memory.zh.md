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

schema v3 新增：

| 字段 | 说明 |
|---|---|
| `event_time_precision` | exact / date / approximate / range / narrative / planned / unknown |
| `event_timezone` / `event_time_source` | IANA 时区；user_stated / model_inferred / imported / corrected |
| `valid_from` / `valid_to` | 有效期区间，修正后旧值仍可回溯 |
| `pinned_core` | 受保护的核心规则，自动写入不能覆盖 |
| `sources.evidence_text` | 记忆对应的原句证据 |
| `memory_relations` | 类型化关系边（supersedes / contradicts / related_to / caused_by / influenced_by / fulfilled_by / next_scene / part_of） |
| `memory_operations` | 治理日志；forget 只存内容指纹，不存正文 |

## 自动抽取

每轮结束后只处理明确的用户陈述：句首的 `记住/记一下/别忘了`、称呼、偏好/过敏、生日/纪念日和带日期的事件。普通聊天、角色扮演关键词（`设定/扮演/剧情/人设/世界观/虚构/故事线`）以及助手回复都不会被自动保存。

写入防火墙在这些分支之前执行：

- 看起来像台词或设定稿的句子（引号或 `【...】` 占正文一定比例）整体跳过。
- `记住` 只在句首生效，所以角色扮演里的“记住，我所有的反抗……”不会变成用户画像。
- 称呼提取忽略引号内的内容和「你叫我什么？」这类疑问句。
- 情绪短句（`今天好累`、`抱抱`、`想你了`）只更新带 24 小时 TTL 的 `current_mood`，不落条目。

## 去重与冲突

写入时按顺序执行：

1. 归一化 + `sha256` 精确去重。
2. 已知模板折叠（例如旧版“判断是否值得长期记住”的重复任务）。
3. 中文 bigram Jaccard ≥ 0.75 或 SimHash Hamming ≤ 3 近似合并。
4. 同 `realm+subject+predicate` 不同值触发 supersede，旧值保留为历史。
5. `unknown` 默认进入待审核。

## 搜索语义

`memory` 工具的 search 面向管理检索：默认查全部 layer，支持 `realm` / `layer` / `predicate` / `facet` / `status` 过滤，并且可以看到 private 记忆。提示词注入使用更窄的检索路径：只取 active 的 relationship / episodic / procedural，按相关性过滤，并继续执行 audience / 私有渠道 / fictional 隔离。relationship 条目单次最多注入 2 条，避免长关系设定挤掉事件和流程。

两条路径共用混合候选集：

1. **Anchor**：`facet`、`predicate`、`subject` 或首行 `【...】` 标题的精确/包含命中；pinned 条目命中锚点后无条件置顶。
2. **FTS5**：三个字及以上的短语匹配；没有 trigram 分词器时降级为 `unicode61`。
3. **LIKE**：一到两个汉字的短查询，以及 FTS 命中少于 5 条时的召回兜底。
4. **时间通道**：相对时间短语转成区间，与 `event_at` 或 `valid_from/valid_to` 求重叠。
5. **1-Hop**：从 Top 种子沿 related/caused/fulfilled/next_scene 扩展，目标节点重新做 status / realm / scope / audience 校验。

固定块、边界块、检索块分别记账。边界块有独立上限（`max(200, 固定预算*30%)`），至少保留 1 条，并计入总账，不再静默突破预算。

## 数据库迁移（schema v3）

新版本会把 `memory.db` 从 v1/v2 升级到 v3。v2 带来 `facet`、版本化 persona、`relationship_state.expires_at` 和历史表；v3 增加时间语义、证据链、关系网、治理日志、场景胶囊和 FTS。

迁移分两段：

1. `migrateToV2Locked` 补齐任何缺失的 v2 对象（逐列/逐表守卫）。
2. `migrateToV3Locked` 用事务外算好的列存在性计划补齐 6 个 `entries` 列，然后只执行幂等 DDL（`CREATE TABLE IF NOT EXISTS`、`INSERT OR IGNORE`、`SELECT ... WHERE EXISTS`）。
3. 两段都成功后，才在同一事务里写入 `schema_version=3`。

其他保证：

- 先写 `VACUUM INTO` 备份到 `memory/backups/`，只保留最新 2 份。
- `sources` 重建时保留 v2 唯一键，`INSERT OR IGNORE` 仍然去重；孤儿行数量写入 `meta.v3_dropped_orphan_sources`。
- 断链 `supersedes` 数量写入 `meta.v3_dropped_orphan_supersedes`（参考库为 9 和 129）。
- FTS5 在事务前探测；没有 trigram 时记录 `fts_tokenizer=unicode61`，完全没有 FTS5 时记录 `none`，不会导致数据库打不开。
- 数据修复会归位参考库的 9 条 relationship 与 14 条 `待分类`，把被误存为真实画像的台词改判，恢复被误归档的生日条目（日期从原文派生），并补齐缺失来源；不删除条目。

可以在 `/memory` 的“数据库迁移”页一键执行，也可以运行：

```bash
picoclaw memory migrate
```

## 后台页面

启动 launcher 后打开 `/memory`：

- 概览：条目统计、数据库大小、关系状态、归档操作。
- 数据库迁移：查看 schema 版本、空 predicate、relationship 数量、pinned 核心、关系边、FTS 行数与 tokenizer，并一键备份迁移；同一个按钮覆盖 v1 / v2 → v3。
- 记忆库：服务端分页（每页 100 条）、搜索、筛选、编辑、归档、恢复；表格固定列宽，详情面板独立滚动。
- 审核：确认真实记忆 / 标记虚构 / 标记工作 / 忽略。
- 关系与人格：称呼、阶段、纪念日、边界、人设版本；人设可设置 realm / audience / scope。
- 设置：预算、自动抽取、私有渠道、导入旧 workspace。
- 数据库修复：按 realm / layer / status / audience / 关键词预览并批量迁移；执行前自动用 SQLite `VACUUM INTO` 备份。

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
picoclaw memory render   # 可选 Markdown 导出，不会被运行时读回
```

`memory import` 是唯一会读取旧 `MEMORY.md` 的命令；运行时不读取。`memory forget` 不带 `--yes` 时只预览；`memory replay-forget` 用于从旧备份恢复后重新应用遗忘墓碑。

## 从 0.3.x / 0.4.x 迁移

0.3.x → 0.4.x 用一次 `memory import` 把旧 `MEMORY.md` 导入 SQLite。0.4.x → 0.5.x 不需要手写 SQL：首次打开记忆库会先备份并升级 schema v3；v1 数据库在同一事务里走完 v1 → v2 → v3。数据迁移在 `/memory` → “数据库迁移”执行，或运行 `picoclaw memory migrate`。

1. 停止 gateway 与 launcher。
2. 备份旧 `workspace/memory`。
3. 启动 0.5.10，打开 `/memory` → “数据库迁移”执行迁移；0.3.x 旧 workspace 先运行一次 `memory import`。
4. 用 `PRAGMA integrity_check`、`picoclaw memory audit` 和条目数验证；检查公开渠道看不到 `fictional/private` 伴侣人设。

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
