# PicoClaw 项目工作区指令

本文件是 PicoClaw 仓库的工程规范，供人和 AI 代理共同遵守。与全局 `~/.zcode/AGENTS.md` 同时生效；冲突时以更具体、更严格的本文件为准。

---

## 1. 项目概览

PicoClaw 是一个自托管的个人 AI 助手/虚拟伴侣运行时：Go 编写，单二进制部署，数据落在本地 SQLite 与 JSONL，前端是 React + TypeScript 的 Launcher/Dashboard。

**技术栈**

| 层 | 技术 |
|---|---|
| 后端 | Go 1.24+、Cobra CLI、modernc.org/sqlite（默认无 CGO） |
| 存储 | SQLite（WAL）、JSONL 会话、独立遗忘账本 |
| 前端 | React 19、TypeScript、Vite、Tailwind、Radix UI、pnpm |
| 渠道 | 微信 / QQ / pico / WebSocket / Discord 等适配器 |
| 模型 | OpenAI 兼容、Anthropic、Codex 等 Provider + fallback 链 |

**关键目录**

```text
cmd/picoclaw/                  CLI 入口（internal/memory 为记忆子命令）
pkg/memstore/                  长期记忆核心：schema、迁移、治理、检索、账本、任务
pkg/agent/                     Agent 主循环、prompt 装配、pipeline、记忆运行时
pkg/tools/                     模型工具（memory 工具在 memory.go）
pkg/tools/shared/              工具请求上下文（channel/chat/session/scope）
pkg/session/                   会话抽象与 JSONL 后端适配
pkg/memory/                    JSONL 会话存储（Agent 会话历史）
pkg/seahorse/                  消息/摘要存储与 FTS
pkg/identity/                  channel:id 规范化身份
web/backend/api/               Dashboard HTTP API（含 /api/memory）
web/frontend/                  React 前端（/memory 页面）
docs/                          用户文档、参考、设计、发布说明（中英双语）
```

---

## 2. 构建、测试与运行

**Go 构建必须带 tags**

```sh
go build -tags 'goolm stdjson' ./...
go test  -tags 'goolm stdjson' ./pkg/memstore -count=1 -timeout=120s
```

- `goolm`：纯 Go 的 olm 实现；`stdjson`：标准库 JSON。
- Makefile 默认 `CGO_ENABLED=0`。需要 `-race` 时单独用 `CGO_ENABLED=1`，不能沿用默认构建并声称跑过 race。
- 本机网络受限时使用 `127.0.0.1:7890` 代理；`go-sqlite3` 的 C 编译警告来自可选依赖，可忽略。

**前端**

```sh
cd web/frontend
pnpm run lint     # eslint，0 error 才能提交
pnpm run build    # tsc -b && vite build
pnpm run format   # prettier --check
```

前端没有 test/E2E 脚本；不要编造 `pnpm test`。新增测试框架属于独立工作，需单独确认。

**常用运行命令**

```sh
picoclaw gateway
picoclaw memory migrate | repair | inspect | forget | replay-forget | cleanup | audit | stats
```

---

## 3. Go 代码规范

- 匹配现有风格：小函数、显式错误、`fmt.Errorf("memstore: ...: %w", err)` 前缀。
- 不引入新依赖，除非任务明确要求并说明理由。
- 不新增 service/repository 抽象层；`pkg/memstore` 已有 `MemoryService`（`service.go`）作为治理入口，Store 是仓储层，不要再套一层。
- 禁止 `any` 滥用：前端 TypeScript 禁用 `any`；Go 侧只在 JSON/attributes 边界使用 `map[string]any`。
- 异步 I/O 必须有错误反馈；不允许静默吞错（历史遗留的 `_ =` 只可在明确“尽力而为”的日志/统计路径使用）。
- 注释解释“为什么”，不解释“是什么”；不写 TODO 占位或 `// ... existing code`。
- 每个改动必须能追溯到用户需求；不顺手重构无关文件。

**SQLite 硬约束**

- `Open` 使用 `db.SetMaxOpenConns(1)`。**事务持有连接期间，禁止再调用 `s.db` 查询**（会死锁）；需要在事务前用 pre-plan 把列/表存在性算好，事务内只用 `tx`。
- schema 变更、数据修复、版本号写入必须在**同一事务**；`schema_version` 最后写。
- WAL 模式下复制数据库前先 `Close` 或 `wal_checkpoint(TRUNCATE)`；备份用 `VACUUM INTO`。
- 数据目录 0700、敏感文件（`memory.db`、`forget-ledger.db`、配置）0600。
- FTS 触发器只监听内容列，避免 `use_count` 更新导致索引重建。

---

## 4. 记忆系统契约（必须遵守）

### 4.1 单一事实源

- 运行时长期事实只来自 `workspace/memory/memory.db`。`MEMORY.md`、日志、聊天摘要都不能充当运行时记忆；旧 `MEMORY.md` 只允许通过 `memory import` 显式导入。
- 不存在文件记忆回退：不得恢复 `MEMORY.md`/每日笔记读取逻辑。
- workspace 必须可移植：配置与数据库内不得写入绝对路径；相对 `db_path` 由 `PICOCLAW_HOME`/workspace 解析。

### 4.2 Schema 与迁移（0.7.2 起为硬约束）

1. **只保留 v3 → v4 单段迁移。** 不得恢复 v1 → v2 → v3 旧迁移链；v1/v2 数据库必须明确拒绝，并提示先用 0.6.x 升级到 v3。
2. **禁止在 `Open`/启动路径自动迁移。** 已有 v3 库或缺对象的 v4 库必须以故障关闭模式打开：普通记忆读写返回 `ErrMigrationRequired`，只有 `picoclaw memory migrate` 或仪表盘迁移页可以执行 DDL。全新空库可直接初始化为 v4。
3. **迁移顺序固定：** 只读预检 → `VACUUM INTO` 备份 → 单事务 DDL/数据转换 → 最后写 `schema_version=4`。重复执行必须幂等，第二次零变更。
4. **迁移不得猜测归属。** 旧数据进入 `legacy_unassigned` 隔离域；`sources` 逐字段保留证据原文、模型、prompt 版本、置信度、确认标志与 `message_id`，缺失列才用显式默认值。
5. **账本随库备份。** `forget-ledger.db` 与 `memory.db` 同目录、权限 0600；账本不可用时遗忘等破坏性操作故障关闭，不得静默重建空账本。
6. **文档一致性。** 前端、API 文案、guides、迁移参考与发布说明中不得再出现“v1/v2 → v3”的现行迁移描述；修改迁移行为必须同步 `docs/reference/memory-schema-v4.md`（及 `.zh.md`）、`docs/guides/memory*.md`、`CHANGELOG.md` 与 `docs/releases/`。
7. 迁移/测试入口：`test -tags 'goolm stdjson' ./pkg/memstore`；迁移用例见 `migrate_test.go`（v3 库必须 pending、读写拒绝、显式迁移后可用、自愈需再次显式迁移）。

### 4.3 写入、去重与治理

- 所有入口（Web/API/CLI/工具/导入/抽取）统一走 `MemoryService` 或 Store 的治理方法；不得新增旁路写 SQL。
- 去重身份按域隔离：唯一索引 `(domain_id, content_hash) WHERE status='active'`；模板折叠、近重合并、自动标量替换都限定同一 `domain_id`，不得跨域合并。
- 正文变更走 `Correct`（新版本 + 保留旧证据）；`Update` 只允许元数据，且 `pinned_core` 无 `force` 时拒绝正文/受众/作用域/状态变更。
- 用户确认必须绑定批准事件；不得用 `confidence >= 0.95` 或模型自述冒充确认。
- 模型工具只能预览永久遗忘，不能自批；审批计划带 `expected_revision`，变更后必须报冲突。

### 4.4 遗忘与账本

- `Forget` 顺序：校验 → 写独立 journal（HMAC 选择器 + 目标清单 + request_id）→ 主库事务删除（条目/来源/关系边/胶囊/关系状态与历史/摘要失效）→ 标记 applied → 同事务写入 `forget_cleanup` 任务。
- 账本不存正文；选择器用版本化 HMAC。`ReplayForget` 按账本重放，不依赖主库墓碑。
- 清理任务只消费 `forget_cleanup`；JSONL 只做内容精确匹配删除，无法验证报 `partial/unavailable`，不得假成功。
- 关系状态副本必须随来源记忆一起清理（称呼/生日/情绪等 predicate 映射见 `relationshipKeysForPredicate`）。

### 4.5 检索与权限

- 权限（status/domain/realm/audience）必须在 SQL 的 `LIMIT` 之前生效，不得先取候选再过滤。
- 群聊/公开会话永远不能读取 `audience=private`；`private_channels` 只限制渠道，不替代 chat kind 判断。
- 时间命中是独立相关性依据，不要求正文含时间词；`created_at` 不等于事件时间。
- 1-Hop 与主检索共享去重集合和层配额（relationship ≤2、其他 ≤6）；预算按最终渲染文本计，含分隔符。
- 空上下文不等于管理员：无域绑定时只读 `legacy_unassigned`，不降级为全库视图。

### 4.6 发送点与接收方

- 读取许可 ≠ 外发许可。`memory.allowed_chat_recipients` 非空时，每次实际 Provider 调用（主调用、fallback 候选、流式）都要检查；未授权返回不可重试的 `memory egress denied`，不得静默改投。
- 外部摘要/抽取/embedding、语义召回、主动回忆默认关闭；开启需要独立配置、成本上限与接收方许可。
- 主动回忆默认关闭；到期不自动判违约，发送前重新校验目标、授权、限频与静默时段。

---

## 5. 前端规范

- React Hooks + 函数式组件；样式优先 Tailwind，其次 CSS Modules。
- 组件匹配 `web/frontend/src/components/memory/` 现有写法；页面文案必须与当前 schema/行为一致（迁移页不得再写 v1/v2→v3）。
- API 客户端集中在 `web/frontend/src/lib/memory-api.ts`；新增接口先补类型与方法，再改页面。
- 提交前必须 `pnpm run lint` 与 `pnpm run build` 通过；lint 既有 warning 可以保留，但不能新增 error。

---

## 6. 文档规范

- `/docs` 只保留英文与中文两种语言；中文文件用 `.zh.md` 后缀。
- 记忆行为变更至少同步：
  - `docs/guides/memory.md` / `memory.zh.md`
  - `docs/reference/memory-schema-v4.md` / `.zh.md`（迁移相关）
  - `CHANGELOG.md`（中文）与 `docs/releases/<tag>.md`（中文，发布必需）
- 设计文档在 `docs/design/`；实施状态必须区分“已实施/未实施/实施差异”，不得把计划写成现状。
- 文档不得引用不存在的文件、不存在的命令或未验证的数字；迁移文档中的路径、表名、命令必须与代码一致。
- 不添加官方/社区链接到 `/docs`（按现有文档策略）。

---

## 7. 发布流程

1. 通过测试：目标包 `go test -tags 'goolm stdjson' ... -count=1`，前端 `lint` + `build`。
2. 写中文发布说明 `docs/releases/v<version>.md`（缺失会导致 Release workflow 直接失败）。
3. 更新 `CHANGELOG.md` 顶部为新版本，按“新增/修复/升级/已知边界”组织。
4. 提交（一次一件事）→ `git tag -a v<version>` → push `main` 与 tag。
5. 手动触发 Release workflow（tag push 不会自动发布）：

   ```sh
   gh workflow run release.yml -f tag=v<version>
   ```

6. Release workflow 会 checkout tag 构建 GoReleaser 资产并校验中文发布说明；触发后不轮询、不手改 release notes。发布说明必须在打 tag 前提交，tag 必须指向包含它的提交。
7. 发布范围只到本地提交与 GitHub push/tag/release；**禁止通过 SSH 登录或操作服务器**，线上部署由用户手动完成。

---

## 8. 安全与隐私

- 不硬编码密钥/密码/token；不提交 `.env`、`*.db`、`forget-ledger.db`、私密 workspace。
- 记忆正文不得进入操作日志、指标、错误消息或 release 产物；日志只记录 ID、计数、原因码。
- 记忆内容是非可信数据：`pinned`、`confirmed`、system 段位置都不能授予工具/网络/权限能力。
- 敏感分类只能提高保护级别，不能降低；密码/验证码/API Key/私钥等秘密永不写入长期记忆。
- 不访问/修改真实用户数据库，除非用户明确授权并说明范围；验证优先使用临时副本或合成 fixture。

---

## 9. 常见坑

| 现象 | 原因/处理 |
|---|---|
| `v4 add ...: no such table` | 迁移段先建表再 ALTER；新库由 `bootstrapV4` 初始化 |
| 迁移事务内查询死锁 | `SetMaxOpenConns(1)`；事务内禁止 `s.db` 查询，先算 pre-plan |
| 第二次迁移非零变更 | 违反幂等；所有 DDL/数据转换必须带存在性守卫 |
| 重启后库“自己变成 v4” | 0.7.2 前的缺陷；现在必须显式迁移，见第 4.2 节 |
| 前端迁移页显示旧文案 | 同步 `memory-page.tsx` 的标题、确认框、结果与建议 |
| 前端构建失败 | `cd web/frontend && pnpm run build`；类型错误先修 `memory-api.ts` |
| race 测试失败 | 需要 `CGO_ENABLED=1`，且不是默认 Makefile 配置 |
| 行号在文档中漂移 | 设计文档引用行号必须标注基线提交，不当作永久事实 |

---

## 10. 变更边界

- 改动 ≤3 文件 / ≤80 行（不含格式化）时直接执行；更大范围先列计划与验证点。
- 数据库结构、鉴权、支付/财务、核心算法属于红线；改动需显式授权并给出回滚方案。
- 删除迁移代码、修改发布流程、强制 push/tag、删除 5+ 文件等破坏性操作，先说明影响并等待确认。
- 每完成一步给出可验证证据：测试输出、构建结果、迁移前后计数、发布 run 状态。
