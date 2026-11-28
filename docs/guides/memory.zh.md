# 长期记忆（SQLite）

PicoClaw 使用 SQLite 保存长期记忆，替代旧的 `MEMORY.md` 自由文本模式。记忆库是唯一运行时事实源。`MEMORY.md` 只是可选的手动导出：运行时永远不会读取，即使 `memory.enabled` 为 `false` 也不会回退。

## 可移植性

- workspace 内部不保存本机绝对路径；记忆库、索引、导入报告和会话元数据都可随目录复制。
- 配置里的 `agents.defaults.workspace` 可以写成相对路径 `"workspace"`，运行时按 `PICOCLAW_HOME` 解析。
- 复制前停止进程并确保 WAL 已 checkpoint；需要保留 `memory/memory.db` 与 `memory/forget-ledger.db`（两者必须成对恢复）。
- 复制后按运行服务的用户执行 `chown -R <user>:<group> workspace`。

## 目录

```text
workspace/
├── memory/
│   ├── memory.db          # SQLite 主库（WAL）
│   ├── forget-ledger.db   # 独立遗忘账本（HMAC 身份）
│   ├── backups/           # VACUUM INTO 快照：主库 + 账本
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
  "private_channel_exclusions": [],
  "inject_fictional": false,
  "review_required": true,
  "episodic_retention_days": 365
}
```

- `enabled`：为 `false` 时完全关闭长期记忆，不会回退到 Markdown。
- `budget_tokens`：固定块与检索块的总注入预算。固定块（persona/relationship/profile/boundary）先取总预算的 50%，基础内容再限制为固定块的 70%；`boundary` 参与固定块预算裁剪，但至少保留第一条核心规则，溢出通过 `FixedUsage.CoreOverflow/OverflowTokens` 报告。边界仍受 audience / 渠道可见性 / fictional 过滤。
- `auto_extract`：每轮结束后用规则抽取候选记忆；群聊/公开会话不参与抽取。
- `review_required`：新抽取的未知领域记忆进入待审核队列，不进入提示词。
- `private_channel_exclusions`（0.7.3）：默认所有渠道都能读取 `audience=private` 的记忆；把不想读取的渠道加进排除列表即可，设置页提供默认全选的复选框。旧字段 `private_channels` 已废弃且不再读取。群聊/公开会话无论配置如何始终不可见（0.7.10 起提示词与工具路径同样强制执行）。
- `inject_fictional`：是否把角色扮演/虚构记忆注入真实对话，默认关闭。
- `allowed_chat_recipients`：非空时，每次实际 Provider 调用（主调用、fallback、重试、流式）都检查 `provider` 或 `provider/model`；未列入返回不可重试的 `memory egress denied`。空列表是兼容放行，不是严格默认拒绝。

## 记忆分类

| 字段 | 取值 | 说明 |
|---|---|---|
| `realm` | real / fictional / work / unknown | 真实用户事实 / 角色扮演 / 工作流程 / 待审核 |
| `layer` | persona / profile / relationship / episodic / procedural / boundary | 人设 / 画像 / 关系 / 事件 / 流程 / 边界 |
| `status` | active / pending_review / superseded / archived | 生命周期 |

`relationship_state` 保存称呼、关系阶段、亲密度、纪念日等当前状态；`personas` 保存版本化人设。`predicate` 使用受控词表，`facet` 区分同一 predicate 下的不同侧面；只有 profile 的单值事实或显式 `supersedes_id` 会自动覆盖旧值。`boundary` 规则与基础内容共享固定块预算，但基础内容最多使用固定块的 70%，为边界保留独立额度；第一条核心规则不会被静默丢弃，超出部分记入 `FixedUsage.OverflowTokens` 并置 `CoreOverflow`。

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

`auto_extract` 开启时，turn 结束会写入 `memory_jobs` 的 `extraction` 任务；payload 只含 session、origin event 和来源正文的 keyed digest，不保存聊天正文。worker 从已持久化会话中重新读取消息并校验 digest；成功（包括 0 条候选）写入不含正文的治理回执，载荷无效等永久失败进入 blocked，暂时失败按退避重试。进程退出后未完成任务仍可由后续 worker 处理。

## 来源事件与撤回

0.7.25 起，入站自动抽取和 `memory` 工具写路径都由宿主生成稳定的 `origin_event_id`（`"oe_" + content hash`），模型不能提供或覆盖；来源行指向对应 origin event。同一条事实来自多条消息时会合并为一个条目，但保留多个 `sources`，每个来源各自指向一个 origin event。

`POST /api/memory/origins/{id}/unavailable`（必须 `confirm=true`）记录可信编辑或撤回：只把该 origin event 标记为不可用，然后重新评估条目支持度。仍有其他可用来源时保持 `supported`；没有任何可用来源时把条目置为 `evidence_status=unsupported`、`freshness=stale`，事实本身不删除，留给 owner 复核。

## 去重与冲突

写入时按顺序执行：

1. 归一化 + `sha256` 精确去重。
2. 已知模板折叠（例如旧版“判断是否值得长期记住”的重复任务）。
3. 中文 bigram Jaccard ≥ 0.75 或 SimHash Hamming ≤ 3 近似合并。
4. 同 `realm+subject+predicate` 不同值触发 supersede，旧值保留为历史。
5. `unknown` 默认进入待审核。

## 搜索语义

`memory` 工具的 search 默认查全部 layer，支持 `realm` / `layer` / `predicate` / `facet` / `status` 过滤。0.7 起它使用真实请求渠道，与注入共用同一套可见性规则：`audience=private` 的记忆对所有未排除的渠道可见（`private_channel_exclusions`），群聊/公开会话永远不可见。群聊/公开会话还只允许 `realm=real`：显式请求 `fictional/work` 返回 `ErrRealmNotAllowed`，低层 Store 也在 SQL 中以 `e.realm='real'` 交集强制。提示词注入使用更窄的检索路径：只取 active 的 relationship / episodic / procedural，按相关性过滤，并继续执行 audience / 渠道 / fictional 隔离。relationship 条目单次最多注入 2 条，避免长关系设定挤掉事件和流程。

0.7.20 起，模型只保留 `add / search / inspect / 非核心 correct / legacy capsule / forget 预览` 白名单，全部经过 `MemoryService` 并携带可信 domain / channel / chat kind。`confirm`、归档/恢复、所有关系状态写入，以及任何 force/pinned/boundary 变更都只能由 owner 在 dashboard 或 CLI 执行；模型不能创建执行计划。密码、验证码、API Key、私钥在 `Store.Add` 层直接拒绝，工具、API、自动抽取和导入都不能绕过；英文 `password/passwd/bearer/常见 token 格式` 同样拦截。

FTS/LIKE 的 status/domain/realm/audience 过滤进入 SQL 后再应用候选上限；不可见记录不会先占满 100 条候选、再把合法记忆挤掉。

两条路径共用混合候选集：

1. **Anchor**：`facet`、`predicate`、`subject` 或首行 `【...】` 标题的精确/包含命中；pinned 条目命中锚点后无条件置顶。
2. **FTS5**：三个字及以上的短语匹配；没有 trigram 分词器时降级为 `unicode61`。
3. **LIKE**：一到两个汉字的短查询，以及 FTS 命中少于 5 条时的召回兜底。
4. **时间通道**：相对时间短语转成区间，与 `event_at` 或 `valid_from/valid_to` 求重叠。
5. **1-Hop**：从 Top 种子沿 related/caused/fulfilled/next_scene 扩展，目标节点重新做 status / realm / scope / audience 校验。

固定块（基础内容 + boundary）最多使用总预算的 50%，其中基础内容再限制为固定块的 70%；`budget_tokens` 的剩余额度给检索。boundary 参与固定块预算，但第一条核心规则始终保留，即使它单独超限；裁剪与溢出通过 `FixedUsage.BoundaryCut/OverflowTokens/CoreOverflow` 暴露且为真实赋值。固定块耗尽预算时检索为 0，不再强制注入最低 200 token。过期条目在 Search、FTS/LIKE、时间、1-hop 和 capsule 中都在 SQL 过滤阶段排除。

## 一次性升级向导（0.7.26）

从 0.7.20 升到 0.7.25 时，如果出现 `default_domain_missing`、`entries_without_domain` 或历史 `cancelled_cleanup_jobs`，打开 `/memory` →「数据库修复」→「一次性升级向导」。该向导是显式 owner 操作，不在启动时自动修改数据库。

执行顺序：

1. 预览 Schema、默认域、orphan entries、历史 cleanup job 和所有剩余阻塞；
2. 强制备份主库与独立账本；
3. 执行 v3 → v4（如需要）并恢复已知的 `legacy_unassigned` 默认域，不猜 owner；
4. 把无法验证的历史 `cancelled/blocked/failed` cleanup 任务结案为 `legacy_closed`，记录 `legacy_cleanup_unverified` 审计；
5. 重算恢复门禁。

`legacy_closed` 表示“不再阻塞长期记忆”，**不表示原始会话副本已经验证删除**。报告中会保留 `residual_risk` 和备份路径。向导不会结案 pending/running cleanup、pending/blocked ledger intent 或不支持的 Schema；这些情况仍需要人工处理。

## 数据库迁移（schema v4）

0.7.2 起不再在启动时自动迁移：已有 v3 数据库（或缺对象的 v4 库）会以故障关闭模式打开，普通读写返回 `schema migration required`，需要在 `/memory` → “数据库迁移”或 `picoclaw memory migrate` 显式执行。全新数据库直接初始化为 v4。

本版本只提供 `v3 → v4` 一段升级；旧的 v1 → v2 → v3 迁移链已删除。v1/v2 数据库会被明确拒绝，必须先用 0.6.x 打开一次升到 v3。v4 增加归属域与渠道绑定、处理用途许可、来源事件、依赖/任务/计划表、摘要证据、承诺与回忆，以及独立遗忘账本。

迁移是一段带守卫的升级：

1. 创建 v4 新表（`CREATE TABLE IF NOT EXISTS`）。
2. 按事务外算好的列存在性计划补齐 v4 列。
3. 逐字段重建 `sources`：保留证据原文、模型、prompt 版本、置信度、确认标志与 `message_id`；旧表缺失的列才用显式默认值。
4. 建立隔离域 `legacy_unassigned`，把精确指纹索引改为 `(domain_id, content_hash)`。
5. 整段成功后才在同一事务里写入 `schema_version=4`。

其他保证：

- 先写 `VACUUM INTO` 备份到 `memory/backups/`：主库与 `forget-ledger.db` 各生成一份快照，各自只保留最新 2 份。
- 重复执行安全；当前版本的库第二次运行报告零变更。
- FTS5 在事务前探测；缺失的投影会重建，完全没有 FTS5 时记录 `none`，不会导致数据库打不开。
- 可重建的索引、触发器和 FTS 由同一段幂等逻辑修复；`entries/sources/memory_operations/memory_jobs/memory_change_plans` 等业务、义务或审计表丢失时返回 `non_repairable`，必须恢复备份，不能创建空表冒充修复。
- 0.7.20 起全新空库的 base+v4 建表、默认域、revision 与 `schema_version` 在同一事务完成；部分初始化不会留下“假 v4”。
- 已删除的 v3 数据修复（硬编码改判、生日恢复）不会重放；仍需这些修复的库请先运行 0.6.x。

`forget-ledger.db` 与 `memory.db` 同目录、使用独立 HMAC 密钥。0.7.20 起：

- 迁移/修复备份会同时保存主库与账本快照；恢复时必须成对使用。
- 已有 v4 库的账本缺失、key 损坏或身份字段不完整时，读取仍可用，但 `forget` / `replay-forget` 故障关闭，不会静默创建新账本或重置身份。
- 全新数据库和 v3 → v4 迁移允许创建第一份账本；并发首次打开通过 `_txlock=immediate` 保证只产生一个身份。
- 0.7.25 起账本新增 `forget_receipts`（按 `backend + locator_key` 记录状态）和 `forget_journal.blocked_reason/updated_at`；这些是独立账本的增量，主库 schema 仍为 v4。
- `replay-forget` 默认只输出 preview；只重放 eligible intent，`blocked/unsupported/unapproved` 一律不执行。manifest v2 逐目标记录 domain 与 `expected_revision`；legacy manifest 没有 revision 时要求内容 HMAC digest proof。API 需 `confirm=true`，CLI 需 `--yes`；blocked/failed/cancelled cleanup 通过 `requeue-job` 创建 successor。
- `memory forget-audit` 只读报告历史 forget 操作，不自动补账本或删除；历史 `content_hash` 没有 domain，不能用于自动清理。
- 恢复后用 `picoclaw memory ledger-verify --backup <账本快照>` 校验 UUID/key_id 与 seq/pending 水位；身份不一致返回非零退出码，不能继续用它重放遗忘。

可以在 `/memory` 的“数据库迁移”页一键执行，也可以运行：

```bash
picoclaw memory migrate
```

完整步骤见 [Memory Schema v4](../reference/memory-schema-v4.zh.md)。

## 后台页面

启动 launcher 后打开 `/memory`：

- 概览：条目统计、数据库大小、关系状态、归档操作。
- 数据库迁移：查看 schema 版本、空 predicate、relationship 数量、pinned 核心、关系边、FTS 行数与 tokenizer，并一键备份迁移；当前按钮执行 v3 → v4。
- 记忆库：服务端分页（每页 100 条）、搜索、筛选、编辑、归档、恢复；表格固定列宽，详情面板独立滚动。
- 审核：确认真实记忆 / 标记虚构 / 标记工作 / 忽略。
- 关系与人格：称呼、阶段、纪念日、边界、人设版本；人设可设置 realm / audience / scope。
- 设置：预算、自动抽取、私有渠道；旧 workspace 导入只提供 CLI 命令说明，不在页面读取宿主机路径。
- 数据库修复：按 realm / layer / status / audience / 关键词预览并批量迁移；执行前自动用 SQLite `VACUUM INTO` 备份。

## CLI

```bash
picoclaw memory migrate
picoclaw memory repair --dry-run
picoclaw memory inspect <id>
picoclaw memory rebuild-fts
picoclaw memory forget <id> [--yes --expected-revision <r>]
picoclaw memory forget-audit --limit 1000
picoclaw memory replay-forget [--yes]
picoclaw memory cleanup          # 消费 forget_cleanup；gateway 启动时也会自动运行
picoclaw memory requeue-job <job-id> --yes   # 为 blocked/failed/cancelled cleanup 创建 successor
picoclaw memory recovery-status             # 输出长期记忆就绪状态；未就绪时非零退出
picoclaw memory ledger-verify --backup <账本快照>
picoclaw memory capsule --preset t0_compact
picoclaw memory import --from /path/to/old-workspace
picoclaw memory audit
picoclaw memory stats
picoclaw memory compact --retention-days 365
picoclaw memory export --out memories.jsonl
picoclaw memory render   # 可选 Markdown 导出，不会被运行时读回
```

`memory import` 是唯一会读取旧 `MEMORY.md` 的命令，且只允许 CLI；HTTP `POST /api/memory/import` 返回 410。导入内容默认 `private`、`pending`、`legacy_unassigned`。`memory forget` 不带 `--yes` 时只预览；真实删除必须同时提供预览中的 `--expected-revision`，修订变化会冲突。`memory replay-forget` 默认 dry-run，`--yes` 才按 manifest 重放。`memory cleanup` 必须离线运行（检测到 gateway PID 会拒绝），同时清理 JSONL 与 Seahorse；存在 `unavailable/outcome_unknown`、missing locator 回执或 pending/running/blocked/cancelled backlog 时返回非零。`memory requeue-job` 为 blocked/failed/cancelled cleanup 创建 successor，旧行标记 `superseded`。`memory recovery-status` 输出就绪状态，未就绪时非零退出。`memory forget-audit` 只报告历史操作，不修改数据。

## 0.7.25 完整性与恢复修复

- **预算**：boundary 纳入固定块预算；基础内容最多占固定块 70%，第一条核心边界即使超限也保留；`FixedUsage` 的 `BaseTokens/BoundaryTokens/TotalTokens/BoundaryCut/OverflowTokens/CoreOverflow` 均为真实赋值，并有回归测试覆盖。
- **Replay**：`replay-forget` 默认只输出 preview；manifest v2 逐目标记录 domain 与 `expected_revision`，legacy manifest 用 HMAC digest proof；`blocked`、`unsupported`、`unapproved` intent 一律不执行。API 需 `confirm=true`，CLI 需 `--yes`；blocked/failed/cancelled cleanup 通过 `requeue-job` 创建 successor。
- **账本与清理回执**：独立 `forget-ledger.db` 新增 `forget_receipts`（按 backend + locator_key 记录状态）和 `forget_journal.blocked_reason/updated_at`；主库 schema 仍是 v4。回执区分 `removed/verified_absent/unavailable/outcome_unknown`，缺少 locator 回执计为 missing，不能按 done 或 removed=0 判定完成。
- **恢复门禁**：`recovery-status` 与启动预检汇总 pending/blocked intent、pending/running/blocked/cancelled cleanup、缺失域等；任一存在时长期记忆 fail closed（`ErrMemoryNotReady`），普通聊天可以无记忆继续运行。`memory cleanup` 在 backlog、blocked、cancelled 或回执不完整时返回非零。
- **requeue**：`memory requeue-job <job-id> --yes` 只接受 blocked/failed/cancelled cleanup，创建 successor 并把旧行标记 `superseded`，保留原审计行与账本批准不变。
- **来源事件**：入站自动抽取与 `memory` 工具写路径由宿主生成稳定 `origin_event_id`；同一事实来自多条消息时保留多个 `sources`。`POST /api/memory/origins/{id}/unavailable` 使没有其他可用来源的条目变为 `unsupported/stale`，不删除事实。
- **导入**：只允许 `picoclaw memory import --from`；HTTP `POST /api/memory/import` 返回 410。导入内容默认 `private`、`pending`、`legacy_unassigned`。
- **群聊/公开聊天**：只允许 `realm=real`；显式 `fictional/work` 返回 `ErrRealmNotAllowed`，低层 Store 的 SQL 同样强制交集。
- **审计**：owner 的 review/persona/审批写入口统一经 `MemoryService` 与宿主审计上下文；模型仍不能自批或创建执行计划。
- **接口**：CLI 新增 `requeue-job`、`recovery-status`；API 新增 `GET /api/memory/recovery`、`POST /api/memory/origins/{id}/unavailable`、`POST /api/memory/jobs/{id}/requeue`；契约见 `docs/reference/memory-v4.openapi.yaml`。
- **历史说明**：以下 0.7.20 记录按当时发布原文保留。后续复测发现 0.7.20 的 Seahorse race 门禁与 Replay 声明并不成立，0.7.25 修复后重新验证；不回写 0.7.20 为当时已知。

## 0.7.20 安全与清理修复

- 已遗忘正文不能经 `Correct` 或公开 API 内部字段复活；`Update` 不再允许改正文、状态或提升 pinned。
- 模型敏感动作改为 owner-only；owner 计划参数过滤内部字段，批准时校验 payload digest/domain/revision，claim 时即保存批准人，`blocked` 只能经新预览显式重新排队。
- 所有 API 写入使用严格 JSON 解码；记忆条目 DTO 不接受 Force/Bypass/domain/来源根等宿主导出字段。
- Seahorse `grep/expand` 只读取可信 Agent+session 对应的 conversation；模型 `all_conversations` 被拒绝，未知 ChatKind fail-closed。
- egress 覆盖 `/btw`、legacy/Seahorse 摘要和 subagent tool loop；策略拒绝不 retry/fallback。`memory.enabled=false` 不再停止已接受的 forget cleanup。
- 来源证据改为宿主生成、按 `origin_event_id` 隔离的版本化根；JSONL 清理在活跃 store 的单 session 锁内原子改写，覆盖重复消息和工具调用文本；Seahorse 清理同事务删除消息、parts、context items 与依赖摘要。
- 设置更新按字段存在性合并，不再因部分请求清空 recipients 或 V4 开关；`memory:null` 和只有 `db_path` 的部分配置保持安全默认。
- 历史 forget 只读审计；Replay 按 manifest domain 修复。全新库初始化原子化，v4 修复先备份，义务/审计表缺失故障关闭。

## 0.7.10 安全修复

- 群聊/公开会话在提示词、检索块和 `memory` 工具中都不再读取 `audience=private`；关系状态与 persona 同样过滤。
- 模型工具统一经过 `MemoryService`；群聊写入、秘密写入和模型自批永久遗忘都被拒绝。
- 单 Provider 非流式主调用补齐发送点门禁；每次真实 Provider 调用都检查 `allowed_chat_recipients`。
- 账本缺失/损坏不再静默新建；迁移/修复备份同时保存账本，并提供 `ledger-verify`。
- `Correct` 单事务并继承 domain/sensitivity/pinned；摘要发布校验 generation/成员并在写入时失效依赖摘要。
- cleanup 的 partial 任务标记 `blocked`；gateway 启动与每 5 分钟自动消费。
- 数据目录 0700、主库/备份 0600，debug 日志不再输出记忆正文。

## 迁移到 0.7.0

0.3.x → 0.4.x 用一次 `memory import` 把旧 `MEMORY.md` 导入 SQLite。0.4.x → 0.5.x 在首次打开时升级到 schema v3；0.7.0 只做 v3 → v4。v1/v2 数据库必须先经过一次 0.6.x，因为旧迁移链已不再随包发布。

1. 停止 gateway 与 launcher。
2. 备份旧 `workspace/memory`（账本 `forget-ledger.db` 一旦存在也必须一起备份）。
3. 启动 0.7.0，打开 `/memory` → “数据库迁移”执行迁移；0.3.x 旧 workspace 先运行一次 `memory import`，v3 之前的库先运行一次 0.6.x。
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
POST   /api/memory/import          # 410：仅允许 CLI
GET    /api/memory/export
GET    /api/memory/stats
GET    /api/memory/recovery
POST   /api/memory/origins/{id}/unavailable
POST   /api/memory/jobs/{id}/requeue
```

完整的 owner API 契约见 [memory-v4.openapi.yaml](../reference/memory-v4.openapi.yaml)。
