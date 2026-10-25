# 变更日志

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
