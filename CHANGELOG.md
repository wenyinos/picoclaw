# 变更日志

## v0.7.3

### 修复

- **私密渠道改为排除制（默认全选）**：`audience=private` 的记忆对所有渠道可见，改为用 `private_channel_exclusions` 手动排除个别渠道。修复升级 0.7.x 后非 pico 渠道（如 QQ）`memory search` 零命中、旧 private 记忆不可见的故障；旧字段 `private_channels` 不再读取。
- **设置页新增「渠道记忆可见性」复选框**：默认全选，取消勾选即排除；设置 API 返回 `available_channels`（内置 pico + 已配置渠道适配器）。
- **群聊/公开会话不再进入自动抽取**：共享对话不会写入个人画像；读取侧群聊仍只允许 `audience=all`，与渠道配置无关。
- **删除 `allow_from` 为空时的重复安全警告日志**：接入行为不变，日志不再周期性刷屏。
- 迁移页与文档中的渠道可见性描述同步为 0.7.3 语义。

### 升级

- 无 Schema 变更（仍为 v4）；直接覆盖二进制即可。
- 旧配置里的 `private_channels` 会被忽略，默认全渠道私密立即生效；如个别渠道不应读取私密记忆，请在 `/memory` → 设置中取消勾选。
- 群聊行为收紧：0.6.x/0.7.2 之前群聊消息可能被抽入个人画像，0.7.3 起不再发生。

## v0.7.2

### 修复

- **启动不再自动迁移**：已有 v3 数据库（或缺对象的 v4 库）以故障关闭模式打开，普通记忆读写返回 `schema migration required`，只有 `picoclaw memory migrate` 或仪表盘迁移页可以执行 DDL；全新空库仍直接初始化为 v4。0.7.0/0.7.1 中“安装重启后库直接变成 v4、看不到迁移步骤”的问题不再存在。
- **启动不再对已有库执行 v4 DDL**：此前的 `Open` 会先创建 v4 空表，迁移未确认就已改变结构；现在只有空库才建 schema。
- **迁移页文案与建议更新为 v3 → v4**：确认框、说明、迁移结果和迁移后建议不再出现 v1/v2 → v3 的旧描述；结果区显示来源行数、已认领域/隔离域与域数量，迁移状态新增域与任务统计。
- `SourcesRebuilt` 语义修正为迁移后 `sources` 总行数，不再误用隔离域条目数。

### 测试

- 新增迁移行为断言：v3 库打开后必须 pending，读取与写入都返回 `ErrMigrationRequired`；显式迁移后才可用；缺对象的 v4 库同样需要再次显式迁移自愈。

### 升级

- 已由 0.7.0/0.7.1 自动迁移到 v4 的库无需处理，数据与备份保持不变。
- 仍是 v3 的库升级到 0.7.2 后，记忆功能会保持关闭，直到执行一次显式迁移；这是预期行为。
- 0.6.x 二进制禁止写 v4 数据库；回滚必须离线恢复备份并重放最新账本。

## v0.7.1

### 新增

- **发送点门禁**：`memory.allowed_chat_recipients` 白名单在每次实际模型调用前检查（主调用、fallback 候选、流式调用），未列入的 provider 返回不可重试的 `memory egress denied`，不会被静默改投。空列表保持兼容模式，不阻断现有部署。
- **跨存储清理执行器**：`forget` 在删除事务内写入 `memory_jobs`（`forget_cleanup`），manifest 保留来源消息定位与证据 HMAC（不存正文）。`picoclaw memory cleanup` 消费任务，对 JSONL 会话做精确内容匹配删除；匹配不到时报告 `unavailable/partial`，不伪造成功。
- **前端治理工作台**：`/memory` 新增“治理与运行”页，展示遗忘账本健康与待清理义务、待审批计划、后台任务统计、记忆域、承诺和阶段摘要，并支持批准计划、重放账本、生成摘要。

### 修复

- 清理任务与遗忘义务在同一主库事务提交，崩溃后不会丢失跨存储清理目标。
- 清理只处理 `forget_cleanup` 类型任务，抽取/摘要任务不会被清理 worker 误消费。

### 升级

- 无 Schema 变更（仍为 v4），无配置迁移。
- 如需启用接收方白名单，在 `config.json` 的 `memory.allowed_chat_recipients` 中列出 `provider` 或 `provider/model`；不配置时行为与 0.7.0 一致。
- 运行 `picoclaw memory cleanup` 可处理升级前遗留的清理义务。

## v0.7.0

### 新增

- **记忆 schema v4**：一次 `v3 → v4` 单段迁移（`memstore.CurrentSchemaVersion = 4`）。新增 owner/域（`memory_domains`）、渠道绑定（`memory_actor_bindings`）、处理用途许可（`memory_processing_grants`）、来源事件（`memory_origin_events`）、依赖表（`memory_dependencies`）、持久任务（`memory_jobs`）、审批计划（`memory_change_plans`）、承诺（`memory_commitments`）、主动回忆（`memory_recalls` / `memory_recall_feedback`）、摘要证据（`memory_summary_evidence`）。
- **旧迁移链移除**：不再包含 v1 → v2 → v3 的迁移代码。v1/v2 数据库会明确拒绝打开，需先用 0.6.x 升级到 v3，再由 0.7.0 迁移到 v4。
- **独立遗忘账本** `memory/forget-ledger.db`：`forget` 先写 journal（HMAC 选择器、目标清单、request_id），再改主库；主库或 outbox 回滚后仍可由账本重放。`replay-forget` 改为按账本 HMAC 匹配，跨备份阻止复活。
- **遗忘联动关系状态**：删除称呼/生日/情绪条目时同步清理 `relationship_state` 与历史，不再出现“忘了记忆但提示词仍叫这个名字”。
- **域内去重**：精确指纹唯一索引改为 `(domain_id, content_hash) WHERE status='active'`；模板折叠、近重合并、自动标量替换全部限定在同一 `domain_id`，不同权限域的同文记忆独立保留。
- **检索正确性**：权限（状态/域/受众）在 `LIMIT` 之前进入 SQL；长查询也保留 LIKE 回退；纯时间命中不再因缺少词面重合被丢弃；1-Hop 与主检索共享去重集合和层配额；预算计入分隔符与实际渲染文本。
- **统一服务层**：`MemoryService`（`pkg/memstore/service.go`）提供授权读写、一次性变更计划（`CreateChangePlan` / `ApproveChangePlan`，带目标 revision 的 CAS 冲突检测）；模型工具只能创建预览，不能自批永久遗忘。
- **阶段摘要与证据**：手工主题/阶段簇、逐 claim 绑定 entry/source、`generation` 递增失效；遗忘或纠错会使依赖摘要立即不可见。默认使用本地规则摘要器，不调用外部模型。
- **承诺与主动回忆**：承诺生命周期 `proposed → confirmed → fulfilled/cancelled`，到期不会自动判违约；主动回忆默认关闭，需要私密目标、每日上限与主题冷却；投递状态区分 `sent` / `delivery_unknown`。
- **持久任务队列**：租约、重试退避、attempts 上限后进入 `blocked`，已接受的遗忘/清理任务不会被容量淘汰。
- **Web API**：`GET /entries/{id}/inspect`、`POST /entries/{id}/correct`、`POST /entries/{id}/forget`（支持 `dry_run`）、`GET /domains`、`GET /ledger/health`、`POST /ledger/replay`、`GET/POST /clusters`、`/clusters/{id}/summaries`、`/summaries/{id}/evidence`、`GET/POST /commitments`、`/commitments/{id}/status`、`GET /recalls`、`/recalls/{id}/feedback`、`GET /jobs`、`GET /plans`、`POST /plans/{id}/approve`。
- **前端 API 客户端**：`web/frontend/src/lib/memory-api.ts` 补齐 inspect / correct / forget / 域 / 账本 / 摘要 / 承诺 / 任务 / 计划方法。
- **配置开关（默认关闭）**：`memory.sensitivity_capture`、`summary_enabled`、`llm_extraction_enabled`、`semantic_retrieval_enabled`、`proactive_recall_enabled`、`proactive_daily_limit`、`proactive_topic_cooldown_days`、`allowed_chat_recipients`、`allow_tool_forwarding`、`ledger_path`。缺失字段保持安全默认。

### 修复

- `memory` 工具改用真实请求渠道上下文（`ToolChannel`）读取，胶囊不再选择“配置里的第一个私密渠道”；无渠道上下文时拒绝敏感操作。
- `PUT /api/memory/entries/{id}` 的正文变化转为版本化 `Correct`，不再原地覆盖；`pinned_core` 条目在无 `force` 时拒绝正文/受众/作用域变更。
- 自愈式来源重建逐字段保留 `evidence_text`、`model`、`prompt_version`、`confidence`、`user_confirmed`、`message_id`；缺失列才补默认值。
- `SetRelationshipFull` 支持 TTL 三态（省略=保留、清除、设置），修复编辑时静默清除原到期时间。
- `TestExtractMoodGoesToRelationshipStateOnly` 改用可控时钟，不再依赖运行日期。
- 迁移报告继续兼容仪表盘输出；`migrate` 命令始终保留一份 `VACUUM INTO` 备份。

### 升级

- **迁移前置**：确认数据库已经是 schema v3（0.6.x 打开过一次）。v1/v2 数据库会被拒绝并提示先用 0.6.x。
- **迁移方式**：启动即自动执行（`Open` 时先备份再迁移），或 `picoclaw memory migrate`；仪表盘“迁移”页签名不变。
- **回滚限制**：v4 数据不可由 0.6.x 二进制直接写入；如需回到 v3，必须离线恢复备份并重放最新 `forget-ledger.db`。备份位置 `memory/backups/`，保留最新 2 份。
- **账本持久化**：`forget-ledger.db` 与 `memory.db` 同目录，权限 0600，必须与数据库一起备份；账本丢失时记忆服务按故障关闭处理，不会静默创建空账本。
- **新增默认关闭能力**：外部摘要/抽取/embedding、语义召回、主动回忆均不随升级启用。

## v0.6.0

### 新增

- 全局系统提示词：Launcher 新增「系统提示词」页面，可维护多套预设并随时切换当前生效项。
- 一处切换、全渠道生效：微信 / QQ / WebSocket / pico 等所有渠道从下一个对话轮次起统一使用新指令，无需重启 gateway。
- 配置独立存于 `~/.picoclaw/instructions.json`（权限 0600），与 `config.json` 解耦。改动通过既有 mtime 缓存失效机制传播，未引入新的热重载通道。
- 自定义指令合并进 `instruction.workspace` 同一个 PromptPart，既有提示词分层与缓存序列保持不变。
- API：`GET /api/instructions`、`PUT /api/instructions`，沿用 dashboard 会话鉴权。

### 修复

- `web/backend/api` 的 `TestMemoryAPI_MigrationStatusAndApply` 原先硬编码 schema 版本 2，改为引用 `memstore.CurrentSchemaVersion`，不再随 schema 升级失效。

### 升级

- 不新增、不修改 `config.json` 字段，无配置迁移；未配置该功能时系统提示词与 0.5.x 逐字节一致。
- `instructions.json` 不会自动创建，首次在页面保存时才写入。文件缺失或损坏时静默降级为不注入，并记录一条 warn 日志。
- 容器部署注意：`docker/docker-compose.full.yml` 仅挂载 `config.json` 与 `workspace`，需额外持久化 `instructions.json` 所在目录，否则重建容器会丢失配置。

## v0.5.10

### 新增

- 记忆 schema v3：`entries` 增加时间语义列与 `pinned_core`；`sources` 升级为证据记录；新增 `memory_relations`、`memory_clusters`、`memory_cluster_members`、`memory_summary_versions`、`memory_operations` 和 FTS5 索引。
- 一键迁移支持 v1 → v2 → v3：DDL 与版本号同一事务，逐列守卫，重复执行安全；备份收敛到 `memory/backups/` 并保留最新 2 份。
- 治理动作：`inspect / confirm / correct / archive / restore / forget`；`forget` 级联清理并按内容指纹阻止复活。
- 时间语义：今天/昨天/上周/上个月/去年夏天等短语解析为 `exact / date / approximate / range / planned`，近似时间不伪造单日。
- 混合检索：Anchor、FTS、LIKE、时间通道、1-Hop 关系扩展；`pinned_core` 命中锚点无条件置顶。
- 场景胶囊：`load_capsule` 支持 `t0` / `t0_compact`，公开渠道整体返回空。
- 三层预算记账：固定块、边界块、检索块分别计数，`memory audit` 输出实测 token。
- CLI：`memory repair`、`memory inspect`、`memory rebuild-fts`、`memory forget`、`memory replay-forget`、`memory capsule`。
- API：`GET /api/memory/ops`、`GET /api/memory/capsule/{id}`。

### 修复

- 台词/设定稿不再写入 `real/profile`；`记住` 只在句首触发，引号与【】内容按比例跳过，称呼提取忽略引号内和疑问句。
- 未知 predicate 保留 `attributes.original_predicate`；白名单补齐 `助手人设 / 系统架构 / 备份规范 / 场景 / 世界观`。
- `pinned_core` 条目不会被自动 supersede，冲突进入待审核，只能 `correct --force` 显式替换。
- 两字中文查询走 LIKE，修复 FTS5 trigram 对「生日」「同意」等短词 100% 失配。
- `use_count` 回写不再触发 FTS 重建。
- 固定注入预算不再被边界块静默突破，边界块有独立上限并计入总账。

### 升级

- 首次打开会先备份并升级 schema；在 `/memory` → “数据库迁移”执行数据迁移，或运行 `picoclaw memory migrate`。
- 迁移幂等，不删除条目；参考库的 9 条 relationship 与 14 条 `待分类` 会归位。

## v0.5.1

### 修复

- 记忆库表格改为固定列宽，状态列不再被挤出，重要度列不再被压扁。
- 类型列显示 predicate / facet；长文本截断时保留悬停完整内容提示。
- 记忆详情面板限制最大高度并支持内部滚动，长文本编辑时底部字段仍可访问。

### 升级

- 无数据库或配置文件结构变更。
- 更新 launcher 二进制并重启即可。

## v0.5.0

### 新增

- 记忆库 schema v2：`entries.facet`、`personas` 的 `(id, version)` 与 `realm/audience/scope`、`relationship_state.expires_at` 和 `relationship_state_history`。
- Launcher `/memory` 增加“数据库迁移”页，可一键备份并升级 schema v2；新增 `GET /api/memory/migration/status`、`POST /api/memory/migration/apply`。
- CLI 新增 `picoclaw memory migrate`。
- `memory` 工具支持 `subject`、`predicate`、`facet`、`value`、`supersedes_id`、`emotional_weight`、`valence`，以及 `update_state`。
- relationship 条目进入提示词检索候选，单次最多 2 条；注入命中后异步批量回写 `use_count/last_used_at`。
- 移除未使用的 `/api/memory/reindex` 空接口。

### 修复

- persona 注入按渠道、realm 和 audience 过滤；`fictional/private` 伴侣人设不再进入公开渠道。
- `personas` 同 id 多版本不再撞主键。
- supersede 只对 profile 单值事实自动生效；其他层需要显式 `supersedes_id`，避免同一 predicate 的不同侧面互相覆盖。
- 空 predicate 统一为“待分类”，未知 predicate 不再产生新的垃圾桶键。
- 迁移会把参考库 v13–v21 的 9 条 relationship 写入 predicate/facet，并保持 active。

### 升级

- 无配置文件结构变更；首次打开记忆库会创建 schema v2 备份并升级 schema。
- 在 `/memory` → “数据库迁移”执行一次数据迁移，把空 predicate 标记为“待分类”。

## v0.4.6

### 修复

- `boundary` 从固定块预算中独立出来，单独拼接在固定上下文末尾；大 profile / relationship 不再挤掉边界规则。
- `boundary` 注入增加 `audience` / `private_channels` 过滤，`private` 边界只进入私有渠道。
- `boundary` 注入增加 `inject_fictional` 过滤，未开启虚构注入时 `fictional` 边界不会进入真实对话。
- 新增回归测试：大 profile 不会挤掉 boundary；private / fictional boundary 不会在错误渠道出现。

### 升级

- 无数据库 schema 或配置文件结构变更。
- 更新 gateway / launcher 二进制并重启即可。

## v0.4.5

### 修复

- `memory` 工具的 search 改为跨层检索，默认覆盖 persona / profile / relationship / episodic / procedural / boundary，并支持 `realm` / `layer` / `status` 过滤。
- 修复工具搜索忽略 `realm/layer` 参数、只返回 active `episodic/procedural` 记录的问题；生日、画像、关系设定和 private 记忆现在可以被工具搜索到。
- 修复提示词检索返回无关记忆的问题：query 非空时，相似度为 0 的候选不再注入；移除 SQL 预 `LIMIT`，避免较早的相关记忆被截断。
- 保留提示词检索的 audience、私有渠道和 fictional 隔离；单字中文查询（如“海”）现在也能命中相关事件。
- 修复 `memory search` 在任何查询下都返回同一组 work/procedural 记录的问题。

### 新增

- Launcher `/memory` 增加“数据库修复”：按 realm / layer / status / audience / 关键词预览匹配，批量迁移分类；执行前自动用 SQLite `VACUUM INTO` 备份。
- 新增 `POST /api/memory/repair/preview` 和 `POST /api/memory/repair/apply`。

### 升级

- 无数据库 schema 或配置文件结构变更。
- 更新 launcher / gateway 二进制并重启即可。
- 生产数据如果仍是旧的 realm/layer 分布，可在 `/memory` 的“数据库修复”页面分批迁移；不需要手写 SQL。

## v0.4.4

### 修复

- 运行中的 gateway 会自动加载 CLI 对 `cron/jobs.json` 的新增、删除、启用和禁用，约 500ms 内生效，不再需要重启。
- gateway 停机期间到期的一次性任务会在启动后立即执行一次，不再永久丢失。
- 失败的一次性任务保留为 disabled，写入 `lastStatus`/`lastError`，可通过重新启用立即重试。
- Agent 调用、shell 命令和消息提交失败会真实写入任务状态；`cron list` 显示模式、上次运行、状态和错误信息。
- 配置重载时停止旧的 Cron/Heartbeat 调度器，避免新旧循环重复执行同一任务。
- `tools.cron.enabled=false` 时不再让已持久化任务静默空转，执行器仍会安装。
- 删除最后一个任务后 `jobs.json` 写入 `[]`，不再写入 `null`。
- Cron 日志接入项目 logger，失败和重载信息进入 gateway 日志。

### 新增

- Cron 任务支持 `deliver: true` / `--deliver` 直接投递，不经过 Agent 推理。
- CLI `cron add` 要求 `--channel` 和 `--to`，避免任务执行后没有投递目标。
- 定时任务文档补充热加载、过期补执行、失败保留、直接投递和超时说明。

### 升级

- 无配置或数据库结构变更。
- 更新 gateway / launcher 二进制并重启即可。

## v0.4.3

### 修复

- 记忆库列表改为服务端分页，每页 100 条，支持上一页、下一页和当前页码显示。
- 搜索或切换领域、类型、状态筛选时自动回到第 1 页，并滚动到列表顶部。
- 下一页通过多取 1 条判断是否还有数据，避免最后一页出现空白页。
- 记忆页面改为整页滚动，移除表格固定 520px 高度；宽屏下编辑面板保持吸顶，所有记录都可以通过翻页和页面滚动访问。

### 升级

- 无配置或数据库结构变更。
- 更新 launcher 二进制并重启即可。

## v0.4.2

### 变更

- 移除旧 `MEMORY.md` 与每日笔记的运行时兜底。SQLite 记忆库是唯一记忆源；`memory.enabled: false` 或数据库不可用时不再注入文件记忆。
- 系统提示不再要求模型更新 `MEMORY.md`，改为使用 `memory` 工具；workspace 提示中不再列出旧文件路径。
- 删除旧 `MemoryStore`，`MEMORY.md` 不再参与系统提示缓存失效；新增回归测试，确保旧文件不会进入提示词。
- 文档补充 `goolm,stdjson` 构建标签说明：直接调用 Go 时需要显式传入，`make build` 已包含。

### 升级

- 无配置或数据库结构变更。
- 可以直接删除 `workspace/memory/MEMORY.md`；运行时不会读取，也不会自动重建。
- `picoclaw memory import` 仍是显式的一次性旧数据导入命令；`picoclaw memory render` 仅用于手动导出，导出结果不会被运行时读回。

## v0.4.1

### 修复

- 普通聊天不再进入记忆待审核池。抽取器不再把“设定”“扮演”“剧情”“人设”“世界观”“虚构”“故事线”等常见角色扮演词当作记忆触发条件。
- 不再扫描助手回复中的记忆关键词；只有用户明确陈述才会生成记忆。
- 自动抽取仅处理明确的“记住/记一下/别忘了”、称呼、偏好/过敏、生日/纪念日和带日期的事件。

## v0.4.0

### 新增

- SQLite 长期记忆库（`workspace/memory/memory.db`），支持分层条目（`real/fictional/work/unknown`）、关系状态、人格、边界和来源追踪。
- 记忆去重：精确哈希、模板折叠、中文 bigram 相似度、SimHash 近似去重和冲突替代。
- 记忆审核队列（`pending_review`），不确定的记忆需人工审核后才进入提示词。
- Launcher 记忆页面（`/memory`）：概览、搜索编辑、审核、关系与人格、设置。
- `picoclaw memory` CLI：`import`、`audit`、`stats`、`compact`、`export`、`render`。
- `/api/memory/*` 关系与人格记忆接口。
- 0.3.x workspace 一次性导入：任务记录、会话摘要和旧 `MEMORY.md` 条目。
- 可迁移 workspace：`agents.defaults.workspace` 支持相对路径并按 `PICOCLAW_HOME` 解析，记忆库不保存机器相关绝对路径。

### 性能

- Pico 流式中间帧限流，减少高频全量快照。
- 最终流式帧先于会话保存和压缩发送。
- Pico WebSocket 写操作增加超时，慢客户端不再无限阻塞 agent worker。
- 模型 HTTP 连接池复用，减少并发会话的 TLS 握手。
- 会话 JSONL 追加改为批量异步写入，历史保存不再占用回复热路径。
- 单轮内复用工具定义。
- INFO 级别记录 TTFT 和 chunk 指标。

### 修复

- 修复异步子任务交付、渠道关闭和 agent 测试辅助代码中的数据竞争。
- 会话 API 不再在缓冲写入完成前读取 JSONL 文件。
- 记忆导入可重复执行，不再重复创建 pending 摘要或旧任务。
- 注入前按 `audience` 和 `private_channels` 过滤私密记忆。
- 导入时折叠旧任务模板并跳过占位条目。

### 变更

- README 围绕 0.4.0 重写；项目入口文档只维护英文和简体中文。
- SQLite 记忆启用后，运行时不再读取旧 `MEMORY.md`。
- 0.3.x 配置没有 `memory` 节时，加载时自动补默认值。

### 迁移

0.3.x → 0.4.0 **没有自动 schema 迁移**。详见 [从 0.3.x 迁移到 0.4.0](docs/migration/0.4.0.md) 和 [长期记忆指南](docs/guides/memory.md)。

### 不兼容变更

- 不再发布 Linux/mipsle 二进制：纯 Go SQLite 驱动不支持 MIPS32。
- 长期记忆状态保存在 `workspace/memory/memory.db`。
- 旧 `MEMORY.md` 内容如需保留，必须导入一次。
- 复制或导入 workspace 时必须停止 gateway 和 launcher。

## v0.3.3

### 变更

- 适配 OpenCode Go 的会话请求头：自动注入 `x-opencode-session`，修复 `400 MissingSessionID`。
- Launcher 应用蓝色亮色/暗色主题。

## v0.3.2

### 新增

- 可配置的默认 fallback 模型链。
- Pico 渠道逐轮 token 用量。
- DeltaChat 初步支持。

### 修复

- QQ 网关鉴权方案失效时自动重试。
- 修复 `write_file` 覆盖引导、`exec` 自定义 allow/deny 规则。
- 修复 LINE 响应体关闭错误处理。
- 修复 launcher setup CSRF、pnpm lockfile 重复条目、跨平台路径等问题。

### 变更

- 发布流程改为仅发布二进制包，停止 Docker Hub 发布。
- 升级 Anthropic SDK、golang.org/x/crypto、React 相关依赖等。
