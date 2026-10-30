# 变更日志

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
