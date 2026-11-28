# PicoClaw 项目工作区指令

本文件是 PicoClaw 仓库的工程规范，供人和 AI 代理共同遵守。与全局 `~/.zcode/AGENTS.md` 同时生效；冲突时以更具体、更严格的本文件为准。

**本次核查基线：0.7.26，代码提交 `17214345`，分支 `feat/memory-upgrade-wizard-0.7.26`；主库 Schema v4，0.7.26 新增「数据库修复」一次性升级向导，不改变主库版本。** 该基线用于说明实现状态，不是后续任务可以跳过源码核对的依据。

## 0. 开始工作前

- 先核对当前分支、HEAD、上游和工作区 diff；保留用户已有的修改，不根据压缩对话、旧版本号或旧设计结论假定当前状态。发现基线变化先说明，不回退代码去迁就旧方案。
- 根 `AGENTS.md` 是开发指令；`workspace/AGENT.md` 是随包分发的运行时模板。两者用途不同，不把仓库指令、审批信息或本机开发路径写入助手的运行时人格。
- 实现事实以当前源码、配置/命令定义和实际验证结果核实；本文件的安全硬约束不因代码尚有旁路而放宽。必须区分“已实现”“工程约束”“已知差异/待实现”，并在交付中给出来源或验证证据。
- 历史设计和 `docs/design/PicoClaw-伴侣记忆系统-V4实施计划.md` 用于追溯需求，不能单凭其中的“已实施”清单宣称当前实现完整，也不能据此自动恢复旧迁移链、增加 Schema v5/v6 或扩大外发授权。
- 开始非小改动前列出范围、依赖、风险、回滚及验证点；任务只要求分析/文档时，不顺带实施功能、修改真实配置、启动 gateway 或执行迁移。
- 网络、仓库外写入、Git 元数据操作依照当前工具权限申请；不通过更换代理、关闭安全检查或扩大文件权限绕过授权。

---

## 1. 项目概览

PicoClaw 是一个自托管的个人 AI 助手/虚拟伴侣运行时：Go 核心以单二进制运行，Launcher/Dashboard 有独立二进制及前端嵌入构建；数据落在本地 SQLite 与 JSONL，前端使用 React + TypeScript。

**技术栈**

| 层 | 技术 |
|---|---|
| 后端 | 最低工具链以 `go.mod` 为准，当前 Go 1.25.13；Cobra CLI、modernc.org/sqlite（默认无 CGO） |
| 存储 | SQLite（WAL）、JSONL 会话、独立遗忘账本 |
| 前端 | React 19、TypeScript、Vite、Tailwind、Radix UI；Node/pnpm 以 `web/frontend/package.json` 为准 |
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

**常用源码入口**

| 修改领域 | 优先核对 |
|---|---|
| 迁移与备份 | `pkg/memstore/migrate.go`、`store.go`、`migrate_test.go` |
| 写入/审批与域 | `pkg/memstore/service.go`、`governance.go`、`domain.go` |
| 召回与上下文 | `pkg/memstore/retrieve.go`、`fts.go`、`relation.go`、`pkg/agent/context.go` |
| 遗忘与会话清理 | `pkg/memstore/ledger.go`、`cleanup.go`、`pkg/memory/cleaner.go` |
| 实际模型发送 | `pkg/agent/egress.go`、`pipeline_llm.go`、`pipeline_streaming.go` |
| 管理入口与契约 | `cmd/picoclaw/internal/memory/command.go`、`web/backend/api/memory.go`、前端 `memory-api.ts` |

---

## 2. 构建、测试与运行

### 2.1 Go 与交付产物

普通开发检查使用默认构建标签；特殊平台以对应 Makefile 目标为准：

```sh
go build -tags 'goolm stdjson' ./...
go test -tags 'goolm stdjson' ./pkg/memstore -count=1 -timeout=120s
```

- `goolm`：纯 Go 的 olm 实现；`stdjson`：标准库 JSON。默认 `GO_BUILD_TAGS=goolm,stdjson`；Android 等专用目标有不同标签，不能机械覆盖。
- 根 Makefile 默认 `CGO_ENABLED=0`、`GOTOOLCHAIN=local`，旧工具链应先升级，不依赖自动下载；Darwin Launcher 等平台目标可能需要 CGO。
- `go build ./...` 是包编译检查，不等于生成完整发布资产。根目录 `make build` 生成核心，`make build-launcher` 生成 Launcher；生成/嵌入流程以根 `Makefile` 与 `web/Makefile` 为准。
- race 必须使用兼容平台及 C 工具链，例如 `CGO_ENABLED=1 go test -race -tags 'goolm stdjson' ./pkg/memstore -count=1 -timeout=5m`；不能沿用默认 CGO=0 并声称跑过 race。
- 下载依赖需要获准的网络/缓存权限。若使用本机 `127.0.0.1:7890` 代理，先确认可用及访问权限，不改写用户代理配置或回显凭据；可选 `go-sqlite3` 的 C 编译 warning 与真正编译错误要分开判断。

### 2.2 前端

遵循 `web/frontend/package.json` 的 `packageManager` 和 `engines`：当前 pnpm **10.33.0**，Node 支持 `^20.19.0 || ^22.13.0 || >=24`，建议与 CI 一致使用满足约束的 Node 22。

在 `web/frontend` 执行：

```sh
pnpm install --frozen-lockfile
pnpm run lint
pnpm run build
pnpm run format
```

- 已安装且锁文件未变时不必重复安装；不能为安装成功顺手升级依赖或修改 `pnpm-lock.yaml`。
- `lint` 是 ESLint，`build` 是 `tsc -b && vite build`，`format` 是 `prettier --check`。`pnpm run check` 则会执行 `prettier --write` 与 `eslint --fix`，不是只读检查。
- 普通前端 `build` 不更新后端嵌入目录；需重建嵌入资源时使用 `pnpm run build:backend`，它会清空并重建 `web/backend/dist`。不要把生成资产当作无关源码提交。
- 前端没有 test/E2E 脚本；不要编造 `pnpm test`。新增测试框架属于独立工作，需单独确认。

### 2.3 按范围验证

| 改动范围 | 最低验证路径 |
|---|---|
| 仅工程指令/Markdown 文档 | `git --no-pager diff --check`、`make lint-docs`，另核对相关命令、路径、链接和现状说明；不因此自动跑业务构建或迁移 |
| memstore 治理/迁移/检索 | 上述目标包 `go test`；使用临时 SQLite、合成 fixture、最终结果与副作用断言 |
| agent、工具、身份、会话联动 | 在 memstore 测试之外，按影响运行 `./pkg/agent`、`./pkg/tools`、`./pkg/tools/shared`、`./pkg/identity`、`./pkg/session`、`./pkg/memory`、`./pkg/seahorse` 的带标签测试 |
| API/CLI | 增加 `./web/backend/api`、`./cmd/picoclaw/internal/memory` 的带标签测试及真实请求/命令契约验证 |
| 前端代码、依赖或构建配置 | `pnpm run lint`、`pnpm run build`；改格式时再查 `pnpm run format`，必要时人工 UI smoke |
| 并发、恢复、发布 | 按影响追加 race、真实临时文件恢复/故障注入；正式发布另走第 7 节门禁 |

- 测试应控制时钟、时区和随机种子；不要用固定日期生成短 TTL，再按真实日期读取。检索/benchmark 要断言实际存量与最终命中，不把 Add 次数当规模。
- `make check` 包含依赖下载和格式化；`make fmt`、`make fix`、前端 `check` 会改文件。根 `make test` 还会调用 Web 检查；Docker 集成测试用 `make integration-test`，需要 Docker Compose。执行前先确认范围和副作用。
- 纯文档任务可只做文档验证；不能以 docs lint 代替业务测试，也不能把既有失败藏起来。记录实际执行命令、通过/失败/未运行及原因；超时或中止不算通过。

### 2.4 运行与维护入口

`picoclaw gateway` 会使用真实运行目录，不作为文档检查或随手 smoke 命令。先用 `picoclaw memory --help` 或具体子命令的 `--help` 核对参数。

| 子命令 | 用途与副作用 |
|---|---|
| `inspect ID`、`audit`、`stats` | 检查数据；仍需确认访问的是获准的数据目录 |
| `migrate` | 显式备份/DDL/数据迁移，执行前需要数据操作授权 |
| `repair` | 可修复数据并进入显式迁移维护路径，不是只读诊断 |
| `forget ID` | 默认预览；`--yes` 才执行，不能由模型参数创造审批权限 |
| `replay-forget`、`cleanup` | 重放 eligible 删除/消费清理任务，均可能修改持久数据，不可当作查询命令；`cleanup` 在 backlog 或回执不完整时非零退出 |
| `requeue-job ID` | 仅 blocked/failed/cancelled cleanup；创建 successor 并把旧行标记 `superseded`，需 `--yes` |
| `recovery-status` | 只读就绪状态；未就绪（intent/cleanup 未决）时非零退出 |
| `import --from PATH` | 唯一读取旧 workspace 的入口，仅 CLI；HTTP 导入返回 410 |

不要在 shell 示例里用 `migrate | repair | ...` 表示“任选一个”；`|` 会真的启动管道，可能先执行迁移。

---

## 3. Go 代码规范

- 匹配现有风格：小函数、显式错误、保留 `%w` 错误链；memstore 错误使用 `fmt.Errorf("memstore: ...: %w", err)` 前缀，其他包遵循自身约定。
- 不引入新依赖，除非任务明确要求并说明理由。
- 不新增 service/repository 抽象层；`pkg/memstore` 已有 `MemoryService`（`service.go`）作为治理入口，Store 是仓储层，不要再套一层。
- 禁止 `any` 滥用：前端 TypeScript 禁用 `any`；Go 侧只在 JSON/attributes 边界使用 `map[string]any`。
- 异步 I/O 必须有错误反馈；不允许静默吞错（历史遗留的 `_ =` 只可在明确“尽力而为”的日志/统计路径使用）。
- 注释解释“为什么”，不解释“是什么”；不写 TODO 占位或 `// ... existing code`。
- 每个改动必须能追溯到用户需求；不顺手重构无关文件。

**SQLite 硬约束**

- `Open` 使用 `db.SetMaxOpenConns(1)`。**事务持有连接期间，禁止再调用 `s.db` 查询**（会死锁）；需要在事务前用 pre-plan 把列/表存在性算好，事务内只用 `tx`。
- schema 变更、数据修复、版本号写入必须在**同一事务**；`schema_version` 最后写。
- 在线主库备份用 `VACUUM INTO` 等 SQLite 一致性备份；不能复制运行中单个 `.db` 文件。文件级复制先停写、关闭连接并确认 WAL 已合并；单独做一次 checkpoint 不能防止随后仍有写者。主库快照不包含独立账本或会话副本。
- 数据目录 0700、敏感文件（`memory.db`、`forget-ledger.db`、配置）0600。
- FTS 触发器只监听内容列，避免 `use_count` 更新导致索引重建。

**配置兼容硬约束（0.7.3 事故教训）**

- 配置加载是严格未知字段校验（`pkg/config/diagnostics.go` 的 `collectUnknownJSONFields`）。**禁止删除任何已发布的 JSON 字段**：旧 `config.json` 会因 unknown field 直接加载失败，gateway 无法启动、systemd 反复重启。
- 废弃字段的正确做法：**保留字段定义并标注 Deprecated**；加载时清空值并记录一次性告警；语义切换通过新字段表达（如 `private_channels` → `private_channel_exclusions`）。
- 每次涉及配置字段的变更，必须补一条“上一版本写入的 config.json 仍可加载”的回归测试（参考 `TestLoadConfig_LegacyPrivateChannelsStillLoads`）。
- 发布说明中“忽略/兼容”的表述必须与实测行为一致：写“被忽略”前先验证旧配置真的能加载。

---

## 4. 记忆系统契约（必须遵守）

### 4.1 单一事实源

- 运行时长期事实来自受管 SQLite 主库，默认 `<workspace>/memory/memory.db`，实际按 `memory.db_path` 解析。`MEMORY.md`、日志、聊天摘要都不能充当长期事实源；旧 `MEMORY.md` 只允许通过 `memory import` 显式导入。
- 不存在文件记忆回退：不得恢复 `MEMORY.md`/每日笔记读取逻辑。
- 区分仓库目录、仓库内 `workspace/` 模板与用户运行目录。`PICOCLAW_HOME` 默认 `~/.picoclaw`；`PICOCLAW_CONFIG` 只覆盖配置文件位置，相对 workspace 按 Home 而非配置文件/仓库目录解析，相对 `db_path` 再按 workspace 解析。
- 可移植模板、导出及数据库定位元数据不得硬编码开发机绝对路径。用户运行配置可显式指定受支持的绝对 workspace；运行时解析出的绝对路径不等于应把该路径持久化进可移植证据或提交到仓库。

### 4.2 Schema 与迁移（0.7.2 起为硬约束）

1. **只保留 v3 → v4 单段迁移。** 不得恢复 v1 → v2 → v3 旧迁移链；v1/v2 数据库必须明确拒绝，并提示先用 0.6.x 升级到 v3。
2. **禁止在 `Open`/启动路径自动迁移或自愈业务 Schema。** 已有 v3 库或缺对象的 v4 库必须以故障关闭模式打开，受保护读写返回 `ErrMigrationRequired`。主入口是 `picoclaw memory migrate` 或仪表盘迁移页；`memory repair` 也能进入迁移维护路径，必须按显式数据变更对待。全新空库可直接初始化为 v4；未知/未来版本不得猜测修复。
3. **迁移顺序固定：** 只读预检 → `VACUUM INTO` 备份 → 单事务 DDL/数据转换 → 最后写 `schema_version=4`。重复执行必须幂等，第二次零变更。
4. **迁移不得猜测归属。** 旧数据进入 `legacy_unassigned` 隔离域；`sources` 逐字段保留证据原文、模型、prompt 版本、置信度、确认标志与 `message_id`，缺失列才用显式默认值。
5. **备份/恢复必须保全独立账本及 HMAC 身份。** `forget-ledger.db` 当前与 `memory.db` 同目录、权限 0600，但主库 `VACUUM INTO` 不会包含它。恢复旧主库必须使用最新有效账本，不能拿旧账本覆盖最新墓碑；身份缺失/损坏时应故障关闭，不得静默换新身份。主库与账本没有共同原子事务，恢复后必须用 `ledger-verify` 核对身份并重放未完成的删除义务。
6. **文档一致性。** 前端、API 文案、guides、迁移参考与发布说明中不得再出现“v1/v2 → v3”的现行迁移描述；修改迁移行为必须同步 `docs/reference/memory-schema-v4.md`（及 `.zh.md`）、`docs/guides/memory*.md`、`CHANGELOG.md` 与 `docs/releases/`。
7. 迁移测试入口：`go test -tags 'goolm stdjson' ./pkg/memstore -count=1 -timeout=120s`；用例见 `migrate_test.go`（v3 库必须 pending、受保护读写拒绝、显式迁移后可用、缺对象需再次显式迁移）。维护入口的预检/备份例外不构成完整恢复保证，必须用 `ledger-verify`、`recovery-status` 和恢复演练核对。
8. **0.7.26 一次性升级向导必须保持显式 owner 操作。** 入口是 `/memory` →「数据库修复」：只读预览 → `confirm=true` → 备份主库+账本 → Schema/默认域修复 → 历史 `cancelled/blocked/failed` cleanup 结案 → 重新计算恢复状态。不得在 `Open`/启动时自动执行。无法验证的历史任务只能标记为 `legacy_closed` 并记录 `legacy_cleanup_unverified` 审计，不能标记 `done` 或宣称副本已验证删除；报告必须保留 `residual_risk`。pending/running cleanup、pending/blocked ledger intent、不支持/未来 Schema 和账本不可用时必须拒绝一次性完成，不能顺带结案。

### 4.3 写入、去重与治理

- 所有入口（Web/API/CLI/工具/导入/抽取）必须遵守 `MemoryService` 的授权与治理语义；内部可复用 Store 治理方法，但 Store 不是完整的调用者授权边界，不能因方法可调用就暴露给模型或普通 API。不得新增旁路写 SQL。
- 去重身份按域隔离：唯一索引 `(domain_id, content_hash) WHERE status='active'`；模板折叠、近重合并、自动标量替换都限定同一 `domain_id`，不得跨域合并。
- 正文变更必须走 `Correct`（新版本 + 保留旧证据）；调用侧对 `Update` 只开放元数据白名单，不能假定底层已完全禁止正文更新。`pinned_core` 的正文/受众/作用域/状态变更需要可信批准；模型自报 `force` 不构成批准。
- 用户确认必须绑定批准事件；不得用 `confidence >= 0.95` 或模型自述冒充确认。
- 模型工具只能预览永久遗忘，不能自批；审批计划带 `expected_revision`，变更后必须报冲突。确认、核心修改、遗忘的可信 actor/批准事件来自宿主，不能使用模型生成的 `Force`、`ForceStatus`、`BypassForgetTombstone` 或 confirmed 标志替代。
- 渠道/chat/session/scope 必须从 `pkg/tools/shared` 的请求级上下文取得；查询或工具参数只能缩小权限。不要把管理查询 `Get/List/SearchEntries` 当成普通请求的自动鉴权入口。
- **来源事件必须由宿主生成。** 入站自动抽取（`pipeline_finalize.go`）和 `memory` 工具（`pkg/tools/memory.go`）用 `BuildOriginEventID` 生成稳定 `origin_event_id`，模型不能提供或覆盖。同一事实来自多条消息时保留多个 `sources`；`MarkOriginUnavailable` 只把来源标记不可用并重算支持度，没有其他来源的 entry 置为 `evidence_status=unsupported`、`freshness=stale`，不删除事实。
- **owner 写入口统一审计。** review/persona/审批等 owner 动作经 `MemoryService` 携带宿主 actor、request ID 与 reason code；不得在 API 层直接调用 Store 绕过审计。模型仍不能自批、创建可执行计划或修改 force/pinned/boundary。
- **旧 workspace 导入只允许 CLI。** HTTP `POST /api/memory/import` 固定返回 410；导入内容默认 `private`、`pending`、`legacy_unassigned`，由 owner 复核后再提升，不得由模型触发或读取宿主机任意路径。

### 4.4 遗忘与账本

- `Forget` 顺序：校验 → 独立 journal 写 pending（HMAC 选择器 + 目标清单 + request_id）→ **主库删除、摘要失效与 `forget_cleanup` 入队在同一主库事务提交** → 独立账本 `MarkApplied`。两个 SQLite 库没有共同原子事务，不能把 MarkApplied 与主库入队写成同一个事务。
- 账本不存正文；选择器用版本化 HMAC。`ReplayForget` 按账本重放，不依赖主库墓碑；`applied` 仅表示主库阶段，不等于会话、摘要、备份等外部副本全部清理。
- **Replay 只执行 eligible intent。** 默认只输出 preview；manifest v2 逐目标记录 `domain` 与 `expected_revision`，legacy manifest 没有 revision 时要求内容 HMAC digest proof。`blocked/unsupported/unapproved` intent 一律不执行；API 需 `confirm=true`，CLI 需 `--yes`。blocked/failed/cancelled cleanup 只能通过 `requeue-job` 创建 successor，不能直接改状态或自动解除。
- **主库/outbox 回滚后仍需保留并核验独立删除义务。** 不能因旧主库查不到条目就宣称所有目标已清除；Replay 只重放 manifest 中可证明的目标，其余保持 blocked/unsupported 并报告。
- **账本回执按后端与 locator 记录。** 0.7.25 起独立账本新增 `forget_receipts` 和 `forget_journal.blocked_reason/updated_at`；主库 schema 仍为 v4。回执状态为 `removed/verified_absent/unavailable/outcome_unknown`，缺少 locator 回执计为 missing，不能按 done 或 removed=0 判定完成。
- 清理执行器只消费 `forget_cleanup`。当前 JSONL 依赖 session 定位与整段 Content/ReasoningContent 的规范化 HMAC 匹配，不是逐词语义删除或仅凭 MessageID 精确定位。无法核验必须报告 `partial/unavailable`。
- 必须检查 `CleanupOutcome.Details/Unavailable/OutcomeUnknown`；任务 `done`、退出码 0 或 removed=0 都不能单独证明完整遗忘。`memory cleanup` 在 pending/running/blocked/cancelled backlog 或回执不完整时返回非零。不能声称 JSONL cleaner 同时覆盖 Seahorse、所有摘要、导出和备份。
- **恢复门禁 fail closed。** `recovery-status` 与启动预检在存在 pending/blocked intent、pending/running/blocked/cancelled cleanup、缺失域或账本不可用时把长期记忆置为未就绪（`ErrMemoryNotReady`），普通聊天可无记忆运行；管理路径仍可用于检查和修复。
- 关系状态及历史只按显式来源标记清理；删除时不得按 predicate 猜测映射 nickname/birthday/anniversary，不能据此声称所有情绪/任意副本均自动覆盖。

### 4.5 检索与权限

- 权限（status/domain/realm/audience）必须在 SQL 的 `LIMIT` 之前生效，不得先取候选再过滤。
- 群聊/公开会话永远不能读取 `audience=private`，也不能进入自动抽取；渠道可见性自 0.7.3 起是**排除制**（`private_channel_exclusions`，默认全渠道私密），旧 `private_channels` 白名单字段已废弃，不得恢复。群聊/公开聊天只允许 `realm=real`：显式请求 `fictional/work` 返回 `ErrRealmNotAllowed`，低层 Store 检索也必须以 SQL `e.realm='real'` 交集强制，不能靠上层参数单独兜底。
- 时间命中是独立相关性依据，不要求正文含时间词；`created_at` 不等于事件时间。
- 1-Hop 与主检索共享去重集合和层配额（relationship ≤2、其他 ≤6）；预算按最终渲染文本计，含分隔符。
- 空上下文不等于管理员：普通域感知查询/写入的无域兼容默认是 `legacy_unassigned`，不是所有低层 Store API 都自带该限制。敏感操作缺可信身份时拒绝，不能降级为全库视图或借管理接口扩大权限。

### 4.6 发送点与接收方

- 读取许可 ≠ 外发许可。每次实际 Provider 调用（主调用、fallback、重试、流式）都必须按策略检查；未授权返回不可重试的 `ErrMemoryEgressDenied`（`memory egress denied`），不得静默改投或复用旧上下文。
- 当前配置入口是 `memory.allowed_chat_recipients`：匹配不区分大小写的 `provider` 或 `provider/model`，空列表是兼容放行，**不是已经实现严格默认拒绝**；也不等同于数据库 processing grant 或 endpoint 级授权。
- `budget_tokens=0` 不是关闭记忆的可靠方式：上下文装配会回落到默认固定预算，而现有 egress 检查又以正预算为条件。需要停用时使用明确的 enabled/权限控制并核对运行时是否生效。
- **预算边界契约：** 固定块（persona/relationship/profile/boundary）先取总预算的 50%，基础内容再限制为固定块的 70%；boundary 参与固定块预算裁剪，但第一条核心规则即使超限也保留。`FixedUsage.BaseTokens/BoundaryTokens/TotalTokens/BoundaryCut/OverflowTokens/CoreOverflow` 必须真实赋值，不能用字段存在代替最终装配与账单一致性。
- 当前自动抽取与默认阶段摘要为本地规则；外部抽取/摘要/embedding、语义召回和主动发送不能仅凭配置字段存在就宣称已接通。未来接入需独立用途/接收方许可、成本上限与故障降级，不得默认外发历史。
- 主动回忆候选/状态持久化不等于发送调度器。接通发送前必须默认关闭、重检目标/授权/限频/静默时段；到期不自动判违约。

---

## 5. 前端规范

- React Hooks + 函数式组件；样式优先 Tailwind，其次 CSS Modules。
- 组件匹配 `web/frontend/src/components/memory/` 现有写法；页面文案必须与当前 schema/行为一致（迁移页不得再写 v1/v2→v3）。
- API 客户端集中在 `web/frontend/src/lib/memory-api.ts`；新增接口先补类型与方法，再改页面。
- 修改前端代码、依赖或构建配置时，提交前必须 `pnpm run lint` 与 `pnpm run build` 通过；lint 既有 warning 可以保留，但不能新增 error。仅工程指令/Markdown 改动按第 2.3 节验证，不自动执行前端构建。

---

## 6. 文档规范

- `/docs` 只保留英文与中文两种语言；新增普通中文文档用 `.zh.md` 后缀。译文与英文源并列，项目入口译文放 `docs/project/`；文档按类型组织，遵循 `docs/README.md`，不要新增语言分桶或根目录散落文档。
- 中文版本发布说明使用既有 `docs/releases/v<version>.md` 约定。已有历史设计文件不因命名规则顺手批量改名；模块文档可按现有结构就近维护。
- 记忆行为变更至少同步：
  - `docs/guides/memory.md` / `memory.zh.md`
  - `docs/reference/memory-schema-v4.md` / `.zh.md`（迁移相关）
  - `CHANGELOG.md`（中文）与 `docs/releases/<tag>.md`（中文，发布必需）
- 设计文档在 `docs/design/`；实施状态必须区分“已实施/未实施/实施差异”，不得把计划写成现状。
- 文档不得引用不存在的文件、不存在的命令或未验证的数字；迁移文档中的路径、表名、命令必须与代码一致。
- 不添加官方/社区链接到 `/docs`（按现有文档策略）。
- 新增/移动文档后运行 `make lint-docs`；它只检查部分布局、翻译命名和源文件对应关系，不验证所有链接、内容、示例或文档站构建，需另外核对相关项。
- 工程指令应足够精确且可执行；命令标明工作目录、参数和副作用，数字标明基线/条件。不要把设计目标、计划命令、历史测试输出或配置字段存在写成已验收事实。

---

## 7. 发布流程

**仅在用户明确要求版本发布时执行。** “补全文档并提交推送”不等于授权改版本号、打 tag、触发 Release 或部署；普通提交按第 10 节处理。

1. 通过测试：目标包 `go test -tags 'goolm stdjson' ... -count=1`，前端 `lint` + `build`。
2. 写中文发布说明 `docs/releases/v<version>.md`（缺失会导致 Release workflow 直接失败）。
3. 更新 `CHANGELOG.md` 顶部为新版本，按“新增/修复/升级/已知边界”组织。
4. 提交（一次一件事）→ 带 `-m` 说明的 annotated tag → push `main` 与 tag；避免打开交互式编辑器，不强制移动已有 tag。
5. 手动触发 Release workflow（tag push 不会自动发布）；把下例的版本占位替换成已存在、已核验的实际 tag 后执行：

   ```sh
   gh workflow run release.yml -f tag="v<version>"
   ```

6. Release workflow 会 checkout tag 构建 GoReleaser 资产并校验中文发布说明；触发后不轮询、不手改 release notes。发布说明必须在打 tag 前提交，tag 必须指向包含它的提交。
7. 发布范围只到本地提交与 GitHub push/tag/release；**禁止通过 SSH 登录或操作服务器**，线上部署由用户手动完成。

Release workflow 是构建/发布流程，不替代测试门禁。普通 main push、文档站部署和 nightly 有各自 workflow 触发条件，不能把这些运行等同于正式 Release，也不为一次文档提交手动 dispatch 发布。

---

## 8. 安全与隐私

- 不硬编码密钥/密码/token；不提交 `.env`、`*.db`、`forget-ledger.db`、私密 workspace。
- 记忆正文不得进入操作日志、指标、错误消息或 release 产物；日志只记录 ID、计数、原因码。
- 记忆内容是非可信数据：`pinned`、`confirmed`、system 段位置都不能授予工具/网络/权限能力。
- 敏感分类只能提高保护级别，不能降低；密码/验证码/API Key/私钥等秘密永不写入长期记忆。
- 不访问/修改真实用户数据库，除非用户明确授权并说明范围；验证优先使用临时副本或合成 fixture。
- 模型获得任意同 UID 文件/exec 能力时，可能绕过应用层接口；不得宣传单靠 MemoryService、提示词或前端确认框即可阻止直接读写受管文件。公开渠道工具权限、宿主隔离和可信批准必须独立核验。
- 读取记忆的授权不自动允许将其写入工具参数、子代理任务、浏览器/网络请求或调试输出；内容转发、来源跳转与导出不能扩大受众或接受任意本地路径。

---

## 9. 常见坑

| 现象 | 原因/处理 |
|---|---|
| `v4 add ...: no such table` | 迁移段先建表再 ALTER；新库由 `bootstrapV4` 初始化 |
| 迁移事务内查询死锁 | `SetMaxOpenConns(1)`；事务内禁止 `s.db` 查询，先算 pre-plan |
| 第二次迁移非零变更 | 违反幂等；所有 DDL/数据转换必须带存在性守卫 |
| 重启后库“自己变成 v4” | 0.7.2 前的缺陷；现在必须显式迁移，见第 4.2 节 |
| 启动报 `unknown field(s)` 并反复重启 | 删除了已发布的配置字段（0.7.3 事故）；恢复 Deprecated 定义并加兼容测试，见第 3 节配置兼容硬约束 |
| 前端迁移页显示旧文案 | 同步 `memory-page.tsx` 的标题、确认框、结果与建议 |
| 前端构建失败 | `cd web/frontend && pnpm run build`；类型错误先修 `memory-api.ts` |
| race 测试失败 | 需要 `CGO_ENABLED=1`，且不是默认 Makefile 配置 |
| 行号在文档中漂移 | 设计文档引用行号必须标注基线提交，不当作永久事实 |

---

## 10. 变更边界

- 同时满足改动 ≤3 文件且 ≤80 行（不含格式化）时可直接执行；任一超出先列范围、依赖、风险、回滚和验证点。
- 数据库结构、鉴权、支付/财务、核心算法属于红线；改动需显式授权并给出回滚方案。
- 删除迁移代码、修改发布流程、强制 push/tag、删除 5+ 文件等破坏性操作，先说明影响并等待确认。
- 每完成一步给出与范围匹配的可验证证据：文档检查、测试输出、构建结果、迁移前后计数或发布 run 状态；没有运行就说明，不编造通过。
- 未获明确请求不提交、建分支或推送；已获提交推送授权时核对当前分支、上游及远端，限定本次文件，不使用 `git add .` 混入用户其他改动，也不清空现有暂存区。
- 面向上游社区贡献遵循 `CONTRIBUTING.md` 的 PR 流程；当前工作仓库的推送沿用已确认的分支/上游。远端保护或非快进拒绝时停止并报告，不自动 force push、rebase、移动 tag 或绕过保护。
- Git 元数据写入按工具权限申请；只读 Git 命令使用 `--no-pager`，状态优先使用 `--no-optional-locks`。提交/标签使用消息参数或非交互式编辑器，不让命令等待输入。
- 提交前检查 diff、暂存范围和敏感文件；提交后记录 hash，推送后核实远端分支。普通文档提交不修改版本号、不自动打 tag 或触发正式 Release。

---

## 11. 海报与文案规范

用于把版本内容整理成小红书等技术分享物料。产物放在 `/home/zemi/MyWork/redNote/<主题目录>/`，不提交进代码仓库。

### 11.1 主题与内容

- **只讲技术细节与工程取舍。** 版本迁移流程、升级步骤、"先升 v3 再升 v4"这类发行说明不写进海报。
- 每套默认 3 张：1 张封面（版本定位 + 3 个技术关键词）+ 2 张技术专题（机制、数据结构、参数、失败案例）。
- 一张海报至少包含 3 个信息块或 6 个技术要点；不允许整版只有一句口号。用表名、字段名、索引、参数、命令、数字撑起密度。
- 文案约 800 字符（按总字符计，含拉丁字母、代码与标点，用 `len()` 统计）；第一人称开发者复盘，写具体失败、取舍和代价，不写营销套话。
- 不参考历史海报样式；每套重新确定视觉方向，同一套内三张保持一致。

### 11.2 视觉规范（编辑部蓝图风基线）

- 画布：CSS 1080×1440 px，`--force-device-scale-factor=2` 输出 2160×2880 PNG。
- 配色：纸色 `#f3efe4`、墨黑 `#11100d`、信号橙 `#e4572e`、安全绿 `#2f6b4f`、警示 `#a8410e`；细网格 `rgba(17,16,13,.06)`、60px 间距。
- 结构：2px 外框（inset 54px）、内容区 inset 78px（可用 924×1284）、衬线大标题（Noto Serif CJK SC）、正文 Noto Sans SC、编号/代码 Noto Sans Mono CJK SC。
- **固定坐标布局是硬要求：** 每个区块用 `position:absolute` + 写死的 `left/top/width/height` 排布；高度必须预先按页面预算分配。
  - 禁止依赖内容自然高度（`margin-top:auto`、`flex:1`、`min-height` 撑开）。
  - 禁止响应式/自适应布局（`flex-wrap`、百分比高度、`grid-auto-rows`、媒体查询）。
  - 卡片网格用固定的 `grid-template-columns` + 固定行高，行内文字超长时压缩字号或换行，不允许把整块挤走。
- **每张海报必须填满整页：** 内容从 header 到 footer 连续排布，不允许出现超过约 60px 的空白带；信息不足时增加技术块、代码片段或参数条，而不是留白。
- 自包含 HTML，不引用网络字体/图片；中文必须使用系统已安装的 Noto CJK 字体。
- 标题层级清晰：大标题 > 技术块标题 > 正文/代码；代码块或字段清单用等宽字体与边框区分。

### 11.3 渲染与验收

```sh
google-chrome --headless=new --disable-gpu --no-sandbox --hide-scrollbars \
  --force-device-scale-factor=2 --window-size=1080,1440 \
  --screenshot=03-topic.png "file://$PWD/03-topic.html"
```

- 文件命名：`01-cover.html/png`、`02-<topic>.html/png`、`03-<topic>.html/png`；HTML 源文件与 PNG 一起保留。
- 程序检查：PNG 宽高必须为 2160×2880。
- **版面检查：** 逐张确认内容填满页面、无大片空白、无溢出/裁切/重叠；只检查尺寸不算通过。
- 视觉验收：用 visual-judge 检查溢出、裁切、重叠、中文渲染与留白；用户明确拒绝时再自行检查。
- 文案与海报数字必须与代码一致（表名、命令、默认值），不得写未实现的能力。

