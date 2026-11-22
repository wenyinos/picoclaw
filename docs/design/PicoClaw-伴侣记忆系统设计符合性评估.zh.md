# PicoClaw 伴侣记忆系统设计符合性评估

| 项目 | 内容 |
|---|---|
| 评估日期 | 2026-11-22 |
| 复核日期 | 2026-11-23（同提交；只读数据库副本抽查，Go/前端检查复跑） |
| 评估类型 | 只读源码核查、现有测试验证、设计差距分析 |
| 代码基线 | `main@dc24e727c8223583216b759decc2b1fb18946065` |
| 当前版本依据 | 评估时 `CHANGELOG.md` 顶部为 v0.7.4；0.7.10 已按本报告修复 P0/P1 |
| 设计依据 | 《PicoClaw 伴侣记忆系统设计文档》（2026-11-21，标称对应 0.7.2） |
| 关联设计 | [PicoClaw 伴侣记忆系统 V4 实施计划](PicoClaw-伴侣记忆系统-V4实施计划.md) |
| 工作区约束 | 未修改业务源码、配置或真实用户数据；未启动 gateway；未执行真实迁移、遗忘或清理 |
| 总结论 | **完整设计验收不通过：基础能力大体落地，V4 安全与治理闭环尚未完成** |

> 本文是特定提交上的符合性快照，不是新的实施计划，也不把设计目标写成实现事实。后续代码变化需要重新核对对应条目。
>
> **0.7.10 状态**：本报告列出的 P0/P1 缺口已在 0.7.10 按“不确定行为保持现有实现”修复。本文保留为 2026-11-22 基线的历史快照；当前行为、升级步骤与剩余边界见 [0.7.10 发布说明](../releases/v0.7.10.md)。

---

## 1. 执行摘要

当前实现可以概括为：

> V1—V3 的 SQLite 长期记忆、证据、时间、关系、检索、胶囊和基础治理大多存在；V4 的 Schema、显式迁移、域模型、独立账本、审批计划、摘要证据、承诺/回忆、持久任务、API 与前端工作台也已有代码和测试。但统一身份权限、模型工具治理、发送点门禁、账本灾备、摘要失效与完整遗忘尚未形成端到端闭环。

即使排除设计文档明确列为未实施的外部 LLM 抽取、embedding/向量检索、MMR、Seahorse 精确清理、主动投递调度等内容，**剩余已宣称实施的部分仍存在阻断性差距**。

### 1.1 可以单独认可的成果

- Schema v4 和只接受 v3 → v4 的显式迁移。
- v3/不完整 v4 数据库故障关闭，普通受保护读写返回 `ErrMigrationRequired`。
- 迁移前 `VACUUM INTO`、单事务 DDL/版本号、证据逐字段保留、旧数据进入 `legacy_unassigned`。
- 域内精确/模板/近重去重与自动标量替换。
- Anchor、FTS、LIKE、Temporal、1-Hop 等检索基础，以及固定/边界/检索三层预算框架。
- 本地规则抽取、关系状态历史/TTL、persona 版本和渠道过滤、场景胶囊。
- 独立遗忘 journal、主库删除与 `forget_cleanup` 同事务入队、JSONL 精确匹配清理基础。
- `MemoryService`、变更计划、摘要证据、承诺/回忆持久化、任务租约/重试等基础设施。
- 设计文档附录第 51-52 节中的主要 CLI/API 入口，以及前端“治理与运行”页面。

### 1.2 阻断完整验收的主因

1. 群聊类型和权限域没有贯穿提示词、模型工具和 `MemoryService`，存在私密记忆进入群聊上下文的路径。
2. 模型工具与自动抽取绕过统一治理，模型可直接确认、强制纠正、归档，秘密禁入也不是全入口不变量。
3. 普通单 Provider 非流式主调用没有经过记忆外发检查。
4. 账本缺失会静默新建，HMAC 身份损坏会静默重置；主库备份不包含独立账本与密钥。
5. FTS/LIKE 仍先取有限候选再补权限，不能满足“权限先于 `LIMIT`”。
6. `Correct`、审批计划、摘要 generation/失效和跨存储恢复没有达到声明的事务与失效语义。

---

## 2. 评估范围与方法

### 2.1 覆盖范围

本次覆盖：

- 存储与 Schema：`pkg/memstore/store.go`、`types.go`、`migrate.go`
- 域与授权：`domain.go`、`service.go`、`relationship.go`
- 写入与抽取：`extract.go`、`import.go`、`pkg/agent/memory_runtime.go`
- 检索与注入：`retrieve.go`、`fts.go`、`relation.go`、`temporal.go`、`pkg/agent/context.go`
- 发送门禁：`pkg/agent/egress.go`、`pipeline_llm.go`、`pipeline_streaming.go`
- 遗忘与治理：`governance.go`、`ledger.go`、`cleanup.go`、`pkg/memory/cleaner.go`
- 摘要与任务：`summary.go`、`commitment.go`、`jobs.go`
- 模型工具：`pkg/tools/memory.go`、`pkg/tools/shared/base.go`
- 管理入口：`cmd/picoclaw/internal/memory/command.go`、`web/backend/api/memory.go`
- 前端：`web/frontend/src/lib/memory-api.ts`、`components/memory/memory-page.tsx`
- 配置与测试：`pkg/config`、相关 Go 测试和前端只读检查

### 2.2 方法

1. 从设计目标建立核对项，而不是只检查类型或表是否存在。
2. 沿真实调用链检查宿主上下文、SQL、事务、实际 Provider 调用和管理入口。
3. 区分“数据结构存在”“基础方法存在”“运行时已接线”“语义已闭环”。
4. 阅读关键测试，确认测试是否覆盖真实入口，而非仅测试底层辅助函数。
5. 使用临时测试数据运行现有 Go 测试和 benchmark；评估成稿时未访问真实用户数据库。2026-11-23 复核时按用户提供的只读副本（复制 db/WAL/SHM 到临时目录）做数据抽查，未触碰原库、未引用正文。
6. 对未运行、无法验证或设计明确排除的内容单独标注。

### 2.3 状态定义

| 状态 | 含义 |
|---|---|
| 基本实现 | 主要运行链路和测试证据存在，但仍可能有明确边界 |
| 部分实现 | 有数据结构或局部路径，尚未满足完整设计契约 |
| 未闭环 | 关键安全/治理约束被旁路，不能按已实施验收 |
| 未实现 | 只有配置、表或提案，缺少实际运行能力 |
| 未验证 | 本次没有足够证据或没有执行对应实测，不作通过判断 |

---

## 3. 设计基线说明

设计文档合并了 V1—V4，部分早期章节仍使用现在时描述历史行为，例如：

- `CurrentSchemaVersion = 3`
- `Open` 自动执行 v1 → v2 → v3 迁移
- “v1/v2/v3 一键迁移”
- 迁移检查清单要求 `schema_version=3`

这些内容与第十部分 V4 及当前代码不一致。本评估采用以下解释：

- 第一至第九部分中的 V1—V3 作为历史和基础能力参考。
- 第四十四至四十九章作为当前 V4 目标与实施声明。
- 旧迁移链删除、当前 Schema 为 v4、启动不自动迁移是正确实现，不计为缺口。
- 设计文档“已实施”清单仍需以当前源码重新证明，不能直接当作验收事实。

---

## 4. 总体符合性矩阵

| 设计领域 | 状态 | 当前证据与边界 |
|---|---|---|
| SQLite 单一事实源 | 基本实现 | 运行时长期事实来自 SQLite；旧 `MEMORY.md` 只在显式导入时读取 |
| JSONL/长期记忆双轨 | 基本实现 | JSONL 保存会话，memstore 保存长期事实；跨存储精确删除仍有限 |
| Schema v4 表、列和索引 | 基本实现 | `CurrentSchemaVersion=4`，V4 新表与既有表扩展均存在 |
| 显式迁移与故障关闭 | 基本实现 | v3/缺对象 v4 打开为 pending；显式迁移后可用；账本灾备另列缺口 |
| 证据逐字段迁移 | 基本实现 | `sources` 重建保留正文、模型、prompt、置信度、确认和消息字段 |
| 域内去重 | 基本实现 | 精确、模板、近重和自动标量路径带 `domain_id`；其他运行链路未完全域化 |
| persona/relationship/TTL | 基本实现 | 有版本、历史、TTL 和渠道过滤；关系状态主键仍限制跨域隔离 |
| 群聊/公开会话权限 | **未闭环** | 自动抽取会跳过群聊，但提示词和工具读取没有统一传递群聊类型 |
| 域/身份/处理许可 | 部分实现 | 表和 CRUD 存在；actor binding、processing grant 未参与实际运行决策 |
| 模型工具统一治理 | **未闭环** | 工具直接调用 Store，可自我确认、强制纠正和修改状态 |
| 秘密与敏感信息禁入 | **未闭环** | Service 局部有拦截，Store/工具/自动抽取/API 可绕过 |
| Anchor/FTS/LIKE/Temporal/1-Hop | 部分实现 | 通道存在；FTS/LIKE 的候选 `LIMIT` 仍早于完整权限过滤 |
| 预算与实际 usage | 部分实现 | 三层预算和分隔符记账存在；最终截断前已记录 usage，可能不等于实际注入 |
| 发送点门禁 | **未闭环** | fallback/流式有检查，普通单 Provider 主调用存在直发旁路 |
| journal-first 遗忘 | 部分实现 | 顺序基本正确；账本缺失/损坏处理与恢复核验不达标 |
| JSONL/Seahorse 全量遗忘 | 部分实现 | JSONL 精确匹配有限；Seahorse 未实现是文档已声明边界 |
| `Correct` 事务与版本 | 部分实现 | 新旧版本和边存在；日志另开事务，域与部分保护属性未完整继承 |
| 一次性审批计划 | 部分实现 | CAS 基础存在；模型到持久计划的入口未接通，部分动作没有执行分支 |
| 摘要证据与 generation | 部分实现 | claim 证据可写入；旧草稿拒绝和纠错/改权失效未闭环 |
| 承诺生命周期 | 部分实现 | Update 有证据检查；Create 可直接传终态，未形成严格统一状态机 |
| 主动回忆 | 基础实现 | 候选、去重、反馈和投递状态持久化存在；没有实际调度发送器 |
| 持久任务队列 | 基本实现 | 租约、退避、blocked 存在；实际消费者主要是 forget cleanup |
| CLI/API/前端 | 基本实现 | 主要入口存在；若按完整治理工作台衡量，若干链路只有展示或基础方法 |
| 外部 LLM/embedding/MMR | 未实现 | 设计已明确排除，当前只有配置或任务类型等基础 |
| 性能 SLO | 未验证 | 有 benchmark，但缺少独立、库存断言和完整性能验收 |

---

## 5. 关键缺口与代码证据

### 5.1 P0：可信上下文没有贯穿提示词和模型工具

**设计目标：** 任意入口都从宿主获取可信身份与会话上下文；群聊/公开会话不能读取 `audience=private`，权限域不能串用。

**代码证据：**

- `pkg/agent/context.go:988-1016` 调用 `BuildFixedContext` 和 `BuildRetrievalContext` 时只传 channel、预算、fictional 与渠道排除列表，没有传 `domain_id` 或 `chat_kind`。
- `pkg/memstore/retrieve.go:661-667` 只有 `chatKind=group/public` 才强制只允许 `audience IN ('','all')`。
- 同文件 `669-685` 表明非空且未排除的 channel 默认是 private。
- 固定画像与边界在 `384-399`、`417-435` 使用 `Store.List` 和 channel audience 检查，没有可信群聊类型。
- `pkg/memstore/store.go:919-970` 的管理型 `List` 不应用权限域过滤；固定画像/边界因此不是完整的域隔离读取。
- 检索注入在 `retrieve.go:481-484` 构造 `SearchOptions` 时没有 DomainID，最终由 `96-104` 回退到 `legacy_unassigned`。
- `pkg/memstore/service.go:71-84` 没有把 `mc.ChatKind` 赋给 `opts.ChatKindHint`。
- `pkg/tools/memory.go:219-225` 的模型搜索也只传 channel，没有传群聊类型。

**影响：**

- 群聊来自未排除渠道时，提示词路径仍可能注入私密记忆。
- 普通提示词检索实际偏向 `legacy_unassigned`，而新域数据又可能通过固定画像/边界出现，域语义前后不一致。
- 现有 `TestVisibilityBlocksPrivateInGroup` 直接手工设置 `ChatKindHint`，未覆盖真实 ContextBuilder、Service 或工具入口。

**结论：** 自动抽取的群聊跳过逻辑已经存在，但读取侧设计目标未闭环。

### 5.2 P0：统一授权与治理被多个入口绕过

**设计目标：** Web/API/CLI/工具/导入/抽取遵守统一授权与治理；模型不能自批确认、核心修改或永久遗忘；秘密永不进入长期记忆。

**代码证据：**

- `pkg/tools/memory.go:184-193` 直接 `store.Add`。
- 同文件 `262-271` 直接 `store.Confirm`。
- 同文件 `273-288` 将模型参数 `force` 直接传给 `store.Correct`。
- 同文件 `297-307` 直接 `store.SetStatus`。
- `pkg/agent/memory_runtime.go:70` 直接调用 `store.ExtractTurnAt`，而 `pkg/memstore/extract.go:86-114` 内部直接 `s.Add`。
- `web/backend/api/memory.go:156-172` 的 dashboard Add 也直接调用 `store.Add`；多数其他 API 同样使用 Store，只有计划列表/审批等少数路径使用 Service。
- `pkg/memstore/store.go:490-500` 对空 sensitivity 只补 `normal`，并不会拒绝 `secret`。
- 真正的秘密拒绝位于 `pkg/memstore/service.go:41-55`，不是所有入口共同经过的存储不变量。

**影响：**

- 模型可以自行生成 confirmed 证据，用户确认与可信批准事件没有必然绑定。
- 模型可通过 `force=true` 替换 pinned core。
- 共享聊天写入、秘密禁入和敏感分类不能视为全入口保证。
- 永久 forget 在模型工具中只预览是正确实现，但不能代表整个治理面已经统一。

**结论：** 与“统一 MemoryService 治理”目标不符。

### 5.3 P0：普通主 Provider 调用绕过发送点门禁

**设计目标：** 每次实际 Provider 调用都检查接收方，包括主调用、fallback、重试和流式。

**代码证据：**

- fallback 候选检查：`pkg/agent/pipeline_llm.go:249-254`。
- 流式检查：`pkg/agent/pipeline_streaming.go:81-85`。
- 普通单 Provider 非流式调用：`pkg/agent/pipeline_llm.go:300` 直接执行 `exec.activeProvider.Chat(...)`，该分支之前没有 `checkMemoryEgress`。
- `pkg/agent/egress_test.go` 只验证 `checkMemoryEgress` 辅助函数及空配置/零预算行为，没有验证上述主调用分支。

**补充边界：**

- 空 `allowed_chat_recipients` 是兼容放行，不是严格默认拒绝。
- `budget_tokens<=0` 也会绕过检查：`pkg/agent/egress.go:25-35`。
- `memory_processing_grants` 没有参与这个发送判断，配置白名单与数据库 grant 不是同一套授权。

**结论：** 第四十八章的发送点门禁声明不完整。

### 5.4 P0：账本缺失/损坏时没有故障关闭

**设计目标：** 账本或 HMAC 身份缺失、损坏时关闭受影响能力，不静默创建空身份；恢复必须使用最新有效账本。

**代码证据：**

- `pkg/memstore/ledger.go:364-381` 在账本不存在时直接打开并创建。
- 同文件 `131-181` 在 UUID、key ID 或 key 不完整/不可读时生成并覆盖为新身份，而不是返回错误。
- 旧 journal 仍保存旧 digest，新身份会改变后续 HMAC 计算，历史匹配能力无法据此得到保证。
- `pkg/memstore/migrate.go:156-174` 的备份只有主库 `VACUUM INTO`，没有同时保存独立账本与密钥。
- `pkg/memstore/governance.go:410-421` 只从最近 500 条 intent 收集关系状态清单。
- 同文件 `454-492` 的 replay 主要处理 entries 和 relationship state/history，没有完整重放摘要失效和各后端清理任务。

**影响：**

- 前端“账本丢失时故障关闭”的文案与实际行为不一致。
- 主库和 outbox 回滚后，仍有能力阻止重学不等于所有派生副本都已核验清理。
- 当前测试覆盖的是“完整账本仍在”的恢复路径，没有覆盖缺失、损坏、密钥轮换和超过 500 条 manifest 的场景。

**结论：** 独立账本基础存在，但灾备契约未闭环。

### 5.5 P0：FTS/LIKE 的权限仍晚于候选 LIMIT

**设计目标：** status/domain/realm/audience 在 SQL `LIMIT` 前生效，不可见候选不能挤掉合法结果。

**代码证据：**

- `pkg/memstore/retrieve.go:142` 调用 `searchFTS(..., "", nil)`。
- 同文件 `160` 调用 `searchLike(..., "", nil)`。
- 候选取出后才在 `149-151`、`168-170` 通过 `loadOne(..., base, args)` 补查权限。
- `pkg/memstore/fts.go:46-67` 和 `85-121` 虽然支持调用方传过滤条件，但这里实际没有传。

**影响：** 不可见记录可能先占满 100 条 FTS/LIKE 候选再被丢弃，导致合法记忆漏召。该问题主要是召回正确性，不应误写成已经证明的直接明文泄漏。

**结论：** Anchor 与 1-Hop 的前置过滤不能掩盖 FTS/LIKE 仍未达到 B07/第四十八章约束。

### 5.6 P1：`Correct` 不是单事务，审批计划未端到端接通

**代码证据：**

- `pkg/memstore/governance.go:156` 先通过 `s.Add` 提交新版本和 supersede。
- 同文件 `162-175` 再开新事务写 correct 操作日志；日志失败时纠正已生效。
- `115-156` 没有继承旧条目的 DomainID，空输入最终由 `store.go:490-492` 写入 `legacy_unassigned`。
- `CreateChangePlan` 的仓库内调用只有 `pkg/memstore/v4_features_test.go:156` 的测试和生产定义，未找到模型/API 的实际创建入口。
- `pkg/tools/memory.go:311-329` 的 forget 只返回预览，没有创建持久审批计划。
- `pkg/memstore/service.go:181-185` 允许 `set_relationship` 计划，但 `280-306` 的执行 switch 没有该动作。
- 计划动作执行与计划状态标记也不是同一事务：`307-315` 在动作完成后才更新 executed。

**影响：**

- Correct 的事务声明不成立，并可能改变记忆的权限域。
- “模型预览 → dashboard 审批 → 一次性执行”目前主要是基础设施，而不是完整用户流程。
- 部分计划可能被标记 executed，却没有执行对应业务动作。

### 5.7 P1：摘要 generation 和依赖失效未闭环

**设计目标：** 生成期间成员、内容或权限变化时旧草稿不能发布；纠错、遗忘、归档、改权立即使依赖摘要不可见。

**代码证据：**

- `pkg/memstore/summary.go:290-296` 读取了 generation，但后续 `285-322` 没有使用它做提交校验。
- `PublishSummary` 只读取发布时的当前 generation 和证据，未接收草稿的预期 generation/input fingerprint：`49-166`。
- claim 验证检查 active 和同域，但没有要求证据必须仍属于该 cluster：`79-105`。
- `memory_summary_evidence.source_id` 没有填实际 source，当前主要绑定 entry，而非完整 entry/source 证据：`136-151`。
- `InvalidateSummariesForEntry` 只有定义，未找到生产调用方：`197-213`。
- Forget 自行处理摘要失效：`pkg/memstore/governance.go:269-287`；Correct、SetStatus 和普通 Update 没有统一调用失效逻辑。

**影响：** 旧摘要可能在证据或权限变化后继续保持 active，生成竞态也不能按设计可靠拒绝。

### 5.8 P1：遗忘覆盖仍是有边界的能力

**已实现部分：**

- journal 先于主库变更。
- 主库删除、摘要失效和 `forget_cleanup` 入队在同一主库事务。
- JSONL cleaner 对无法匹配的内容报告 unavailable/partial。

**边界与缺口：**

- `pkg/memory/cleaner.go:103-115` 只匹配整段 Content 或 ReasoningContent 的规范化 HMAC，不是逐词或语义清理。
- `pkg/memstore/store.go:848-857` 对新写入/抽取的 `sources.evidence_text` 截断为 120 runes；迁移重建 `sources` 时保留旧值、不重新截断。实际副本 117 条来源中 96 条为 120 字符或更长（最大 1404），其中新写入的 120 字符记录是截断值，迁移保留的旧值可能仍是全文。对被截断的 evidence_text，长消息的全文 HMAC 与截断证据不同，清理常只能报告无法核验。
- 未找到 gateway 自动消费 cleanup job 的运行链路；主要显式入口是 `picoclaw memory cleanup`。实际副本中 2 个 `forget_cleanup` job 仍为 pending，与这一代码事实一致。
- `pkg/memstore/cleanup.go:114-120` 在没有 locator 时记录 partial detail 后将任务标记 done；`131-135` 对有 unavailable 的结果也标记 done，因此 done 不能解释为所有副本清除完成。
- Seahorse 没有对应的 `CleanLocators` 实现，这是设计文档已声明的未实施项。

**结论：** 可以把当前能力描述为“主库 + 可验证 JSONL 子集的有限遗忘”，不能描述为所有派生数据完整遗忘。

### 5.9 P1：来源事件、身份许可和若干配置仍主要是脚手架

- `memory_origin_events` 的创建和统计存在，但未找到运行时事件生产/撤回/可用状态更新链路；实际副本中该表 0 行。
- `pkg/seahorse/short_engine.go:36-40` 的 `IngestResult` 仍只有消息数与 token 数，消息 ID 没有返回给长期记忆来源绑定；实际副本 `sources.message_id` 非空 0 条。
- `BindActor`、`ResolveOwner`、`HasProcessingGrant`、`VisibilityFor` 未找到实际运行调用方。
- `llm_extraction_enabled`、`semantic_retrieval_enabled`、`proactive_recall_enabled` 没有 Provider 或调度接线，这与设计未实施声明一致。
- 另外，`sensitivity_capture`、`ledger_path`、`allow_tool_forwarding` 也没有找到配置字段之外的运行使用；尤其 Store 默认始终从主库旁推导账本路径。

**结论：** Schema 和配置存在不能证明来源事件、用途许可、暂停采集和主动投递已经实现。

### 5.10 P1：关系状态的跨域隔离受兼容主键限制

设计文档已经承认 `relationship_state` 保留 `key` 主键是实施差异，但该差异有实际后果：

- `pkg/memstore/relationship.go:125-174` 查询旧值和 upsert 都按 key，而不是 `(domain_id,key)`。
- 不同域写同一个 key 会互相覆盖；历史写入还使用新输入的 DomainID。
- `pkg/memstore/governance.go:288-297` 和 `469-475` 按 key 删除关系状态与历史，没有 domain 限定。

**结论：** 这是已声明实施差异带来的真实隔离边界，不能只把它当作无影响的 Schema 命名差异。

### 5.11 P1：最终 usage 与预算仍不完全对应实际注入

- `pkg/memstore/retrieve.go:572-577` 在最终 `trimToBudget` 之前调用 `RecordUsage`。
- 如果最终渲染进一步截断，已记录 ID 不一定对应完整实际注入条目。
- 边界至少保留一条可能超过边界 cap，是明确的安全优先取舍，不应误报为普通溢出缺陷。
- `EstimateTokens` 是字符估算器，只能保证该计数器下的预算，不等于 Provider 真实 tokenizer 的硬上限。

### 5.12 P1：工程隐私约束还有附加差距

以下属于当前项目工程硬约束，不只是早期功能提案：

- `pkg/memstore/store.go:202-204` 数据目录使用 0755，未显式保证 0700。
- 主库打开路径未看到显式 0600 权限保证；备份目录在 `migrate.go:156-171` 使用 0755，备份文件才单独 chmod 0600。
- `pkg/agent/context.go:1087-1092` 在 debug 日志写入 system prompt 预览，其中可能包含长期记忆。
- `pkg/agent/pipeline_llm.go:169-175` 在 debug 级别记录请求消息；`agent_utils.go:424-456` 仅截断，不做记忆正文脱敏。

**结论：** “记忆正文不进入操作日志、敏感目录/文件权限收紧”的工程约束也不能判定为完整满足。

### 5.13 P2：其他功能与展示差异

- `Store.List` 对 `limit>500` 回退为 100：`pkg/memstore/store.go:959-962`；CLI/API export 使用该接口，不能按请求导出任意大结果集。
- `CreateCommitment` 接受调用方直接提供 fulfilled/cancelled，只有 `UpdateCommitmentStatus` 强制 evidence：`pkg/memstore/commitment.go:37-69,74-97`。
- `MarkRecallStatus` 是状态持久化方法，没有执行发送前目标、许可、限频和静默时段复核。
- 前端迁移页 `memory-page.tsx:1163-1169` 的以下文案与代码不完全一致：
  - “legacy_unassigned 认领前只用于审计，不会注入”与当前提示词默认读取 legacy 域不符；实际副本 117 条条目全部位于 `legacy_unassigned`。
  - “账本丢失时故障关闭”与自动新建/重置身份不符。
  - “每次都会先生成新的备份”与 `ApplyMigration` 仅在尚无 `last_migration_backup` 时补备份的逻辑不符。

---

## 6. 已实现能力的具体证据

### 6.1 单一事实源与显式导入

- `pkg/memstore/import.go:22-25` 明确 SQLite 是事实源，旧文件不参与运行时回退。
- 只有 `ImportLegacy` 在 `126-145` 显式读取旧 `MEMORY.md`。
- `pkg/agent/context_memory_test.go:85-109` 验证旧 MEMORY/每日笔记标记不会进入 system prompt。
- 实际副本目录仍保留 27 行 `MEMORY.md`，但按上述代码路径不会进入运行时上下文；它只在显式 `memory import` 时被读取。

### 6.2 显式迁移与证据保全

- `pkg/memstore/migrate.go:14-23` 当前只支持 v3 → v4。
- `store.go:308-332` 对已有不完整数据库设置 pending，而不是自动执行 DDL。
- `migrate.go:381-413` 在单事务内最后写 schema 版本并提交。
- `420-602` 覆盖 V4 列、表、索引和 FTS。
- `608-682` 逐字段重建 sources，并过滤孤儿来源。
- `540-547` 创建 `legacy_unassigned`，不根据文本或会话猜测 owner。
- `pkg/memstore/migrate_test.go` 覆盖 v3 pending、显式迁移、v2/未来版本拒绝、缺对象再次显式自愈和证据保留。

### 6.3 域内去重与基础写入

- `pkg/memstore/store.go:550-639` 的精确、模板和近重路径带 DomainID。
- `799-822` 自动标量替换也限定 DomainID，并处理 pinned 冲突。
- `sources` 唯一键包含 origin event，证据文本仍截断保存。
- `pkg/memstore/v4_features_test.go:12-46` 验证不同域同文独立保留。

### 6.4 检索与上下文基础

- Anchor、FTS、LIKE、Temporal 的候选合流位于 `retrieve.go:56-241`。
- 时间命中可独立保留候选，不再要求正文重复时间词：`174-197,216-224`。
- 1-Hop 在 `relation.go:80-182` 做域、状态、realm、scope 和 audience 复核，并设置全局候选上限。
- 固定/边界/检索三层预算与分隔符估计在 `retrieve.go:355-685` 和 `pkg/agent/context.go:980-1031` 接线。
- 场景胶囊按 ordinal、渠道、fictional 和预算处理：`relation.go:188-269`。

### 6.5 遗忘、任务和治理基础

- `Forget` 的 journal-first 和主库事务主体位于 `governance.go:189-325`。
- 独立账本不存正文，使用 HMAC：`ledger.go:210-215`。
- `cleanup.go:73-84` 将清理 job 与主库删除放在同一事务。
- `jobs.go` 提供持久任务、优先级、租约、重试、blocked 和统计。
- `MemoryService` 的 revision CAS 在 `service.go:252-278`，已有冲突测试。
- 摘要 claim、承诺、recall 和反馈都有实际表读写方法，不能只评价为“空表”。

### 6.6 配置兼容与前端入口

- `private_channels` 保留 Deprecated 字段，旧配置仍能加载：`pkg/config/config.go:67-71,1611-1616`。
- `private_channel_exclusions` 默认空，表示全渠道私密、按渠道排除。
- 自动抽取在 `pkg/agent/pipeline_finalize.go:90-96` 跳过 group/channel/public。
- 设计文档附录列出的主要 CLI 命令均存在，另有 `cleanup`。
- 设计文档附录中的 API 路径均注册，且增加了域、账本、摘要、承诺、recall、job 和 plan 等 V4 路由。
- 前端 `/memory` 有迁移、条目、审核、关系、治理、设置和修复页面。

---

## 7. 设计明确未实施或可选的内容

以下内容不应被误报为“宣称已完成但实际没有”的新发现：

| 内容 | 当前评价 |
|---|---|
| 外部 LLM 抽取 | 未接线；当前是本地规则抽取 |
| embedding/向量检索、sqlite-vec | 未接线 |
| MMR、动态配额 | 未实现，当前是固定配额与分档去重 |
| 助手复述、历史快照逐轮污点追踪 | 未形成完整 MemoryUseManifest/依赖链 |
| Seahorse 逐条消息与叶/祖先摘要精确清理 | 未实现，需要显式 partial |
| 计划审批的独立登录二次确认 | 未实现；dashboard admin context 不等于独立二次认证 |
| 主动回忆实际调度发送 | 未实现；当前是候选、状态和反馈持久化 |
| 演示模拟器、正式性能 SLO 验收 | 未实现或本次未验证 |
| message_id 强绑定 | 未完整实现；Seahorse ingest 仍未返回 ID |
| 主题自动聚类与高质量阶段摘要 | 当前是手工簇和本地规则摘要器，不是完整自动理解系统 |

这些边界意味着，即使修复本报告列出的 P0/P1 问题，也应继续把产品能力表述为分阶段实现，而不是一次完成全部长期记忆愿景。

---

## 8. 测试与实测记录

### 8.1 Go 相关包测试

最终复核执行：

```sh
go test -tags 'goolm stdjson' \
  ./pkg/memstore ./pkg/agent ./pkg/tools ./pkg/tools/shared \
  ./pkg/identity ./pkg/session ./pkg/memory ./pkg/seahorse \
  ./pkg/config ./web/backend/api ./cmd/picoclaw/internal/memory \
  -count=1 -timeout=180s
```

结果：

- `pkg/memstore`：通过。
- `pkg/agent`：通过。
- `pkg/tools`、`pkg/tools/shared`：通过。
- `pkg/identity`、`pkg/session`、`pkg/memory`、`pkg/seahorse`：通过。
- `pkg/config`、`web/backend/api`：通过。
- `cmd/picoclaw/internal/memory`：`[no test files]`。

2026-11-23 复跑同一命令：上述包全部通过，`cmd/picoclaw/internal/memory` 仍为 `[no test files]`。

这只表示本次列出的相关包通过，不等于 `go test ./...`、race、发布构建或完整设计验收通过。

### 8.2 检索 benchmark

执行：

```sh
go test -tags 'goolm stdjson' ./pkg/memstore \
  -run '^$' -bench '^BenchmarkHybridRetrieve$' \
  -benchtime=500x -count=1 -timeout=120s
```

三次结果：

| 次序 | 条件 | p95 |
|---|---|---:|
| 第一次 | 单独运行 | 1.357ms |
| 第二次 | 与其他 Go 测试并行复核 | 10.61ms |
| 复核（2026-11-23） | 单独运行 | 1.193ms |

分析：

- 三次记录都低于设计中的旧 15ms 参考线；2026-11-23 复核数量级与第一次一致。
- 第二次受并发负载影响，说明结果对环境敏感。
- `bench_test.go` 实际构造 201 条 fixture（200 条普通条目 + 1 条 pinned），但没有断言 Add 后的实际库存规模，也没有完整最终注入/权限依赖校验。
- 因此该结果只能作为现有微基准参考，不能宣称已复现旧文档的 0.68ms，也不能替代独立性能 SLO。

### 8.3 前端检查

在 `web/frontend` 执行：

```sh
pnpm run lint
pnpm run format
```

结果：

- ESLint：0 error、8 个 warning，均位于 `memory-page.tsx` 的 Hook 依赖检查。
- Prettier：失败，17 个文件有格式问题，其中包括 `memory-page.tsx` 和 `memory-api.ts`。
- 2026-11-23 复跑结果相同：ESLint 0 error/8 warning，Prettier 仍报告 17 个文件。
- 未执行 `pnpm run check`，避免自动改写文件。
- 未执行前端 build，本报告不声明前端构建通过。

### 8.4 工作区检查

最终执行：

```sh
git --no-pager status --short --branch
git --no-pager diff --stat
git --no-pager diff --check
```

核查前业务工作区干净。导出后仅新增本文档，未修改业务源码或配置。

---

## 9. 测试覆盖盲区

| 盲区 | 当前测试为何不足以证明设计完成 |
|---|---|
| 主 Provider 直发 | egress 测试只调用检查函数，没有覆盖 `pipeline_llm.go:300` |
| 群聊真实运行链路 | 群聊检索测试手工设置 hint，未经过 ContextBuilder/Service/工具 |
| 域隔离提示词 | 域测试主要验证同文去重，没有验证固定/检索注入的域归属 |
| 模型治理授权 | 没有覆盖模型 self-confirm、force、群聊写入和秘密禁入的所有入口 |
| 账本缺失/损坏 | 恢复测试使用完整账本，没有覆盖丢失、坏 key、UUID 不符和历史版本 |
| Correct 故障窗口 | 测试验证最终日志存在，没有注入日志事务失败或域继承检查 |
| 审批完整状态机 | 主要测试 revision 冲突，没有覆盖 set_relationship、执行后标记失败和重复审批 |
| 摘要生成竞态 | 没有 fake summarizer 暂停期间删除/纠错/改权/改成员的测试 |
| 真实跨存储清理 | cleanup 测试主要使用 fake cleaner，未覆盖真实 JSONL 长文和 Seahorse 派生摘要 |
| 性能与故障恢复 | 没有独立 SLO、磁盘满、进程崩溃和多后端恢复演练 |

---

## 10. 验收结论与优先级建议

### 10.1 结论

**完整设计验收：不通过。**

可按阶段认可：

- Schema v4 与显式迁移基础设施。
- SQLite 基础存储、证据、去重、检索和胶囊。
- 本地规则抽取和基础治理入口。
- 独立账本、审批、摘要、承诺/回忆、任务和前端的基础框架。

不能认可为已经完成：

- 全入口统一身份、域、群聊和隐私权限。
- 可信用户确认与核心修改审批。
- 每次实际模型发送的接收方门禁。
- 账本损坏/恢复时的完整遗忘保证。
- 摘要依赖失效和旧草稿拒绝。
- 所有派生数据的完整遗忘。

### 10.2 建议优先级

#### P0：先修复安全与授权闭环

1. 将可信 domain/chat kind/actor/purpose 贯穿 ContextBuilder、检索、工具和 Service。
2. 所有模型写入、确认、纠正、状态变更经过统一治理；秘密禁入成为不可绕过的入口约束。
3. 在普通主 Provider、fallback、重试和流式的每个真实发送点执行同一接收方检查。
4. 账本缺失、损坏、UUID/key 不符时故障关闭；建立账本与密钥的独立灾备和恢复核验。

#### P1：再修复一致性与派生数据

1. FTS/LIKE 在候选 `LIMIT` 前应用完整权限。
2. Correct、计划执行和状态标记满足事务、域继承、一次性与幂等要求。
3. 摘要提交校验 generation、完整输入指纹和证据成员；所有内容/权限/状态变化统一失效依赖摘要。
4. 明确 JSONL/Seahorse/摘要/备份的清理覆盖和 partial 状态，不能以 job done 代表全部副本遗忘。
5. 收紧目录/文件权限和 debug 日志，避免记忆正文进入日志。

#### P2：最后补齐质量与运维证据

1. 为上述真实入口增加回归测试和故障注入。
2. 统一前端、指南、发布说明与实际行为。
3. 建立独立性能 SLO、真实库存断言和恢复演练。
4. 再继续评估外部 LLM、向量、MMR 和主动投递，不把配置开关当成已经接通的能力。

---

## 11. 评估限制

- 评估成稿时未访问真实用户数据库，结论来自当前源码、合成测试和测试代码。2026-11-23 复核时使用用户提供的只读副本（`memory.db` + WAL/SHM + `forget-ledger.db`，复制到临时目录），仅做计数与结构查询，未触碰原库、未引用正文。副本关键计数：`schema_version=4`；entries 117（110 active、7 superseded），全部 `legacy_unassigned`；sources 117（96 条 `evidence_text` ≥120 字符，最大 1404；含截断值与迁移旧值）；`message_id` 非空 0 条；`memory_origin_events` 0 行；`relationship_state` 6 行；forget journal 2 条 applied、0 条 pending；`memory_jobs` 2 条 `forget_cleanup` pending；`memory_summary_versions` 0 行；备份目录只有 2 个主库快照，没有账本或密钥备份。
- 未启动 gateway，未执行真实 Provider 请求，因此没有线上外发抓包证据；发送缺口来自实际调用链静态核查。
- 未执行真实迁移、forget、replay 或 cleanup，避免改变持久数据；数据库副本只做静态查询。
- 未运行 race、完整 `go test ./...`、前端 build 或正式发布流程。
- 未对真实模型 tokenizer、真实群聊适配器逐一做端到端验证；数据库副本的计数不代表其他部署。
- 本报告指出的“未找到调用方”限定于本次当前提交的仓库内搜索，不推断外部私有插件的接线。
- 行号只对上述提交有效；后续修改需重新定位并复核。

---

## 12. 覆盖摘要

本报告已经覆盖设计中的存储、迁移、域/身份、写入、去重、证据、时间、检索、预算、发送、遗忘、治理、摘要、承诺/回忆、任务、配置、CLI、API、前端、测试验证，以及用户提供数据库副本的只读抽查。

最终判断不是“项目没有实现记忆系统”，而是：

> **项目已经实现了一个有相当基础的长期记忆系统，但设计文档把部分 Schema、基础设施和局部测试进一步表述成了完整安全治理闭环；当前代码尚不足以支撑这一完整表述。**
