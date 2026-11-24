# PicoClaw 记忆系统安全与质量整改实施计划

> 文档状态：已实施并发布（grilling 复审通过，版本 0.7.20）
> 代码核查基线：`0431a574`（与 `95520122` 的 Go 实现一致）
> 数据库参考快照：`/home/zemi/下载/memory`，2026-11-22 数据，Schema v4、revision 3840
> 文档生成日期：2026-11-25
> 适用对象：开发、评审、测试与发布人员
> 重要边界：主人已于 2026-11-25 明确授权按本计划实施并发布 0.7.20。参考数据库继续保持只读；真实运行库的迁移、Replay 和 Cleanup 仍必须由用户显式执行。

## 0. 执行摘要

### 0.1 结论

保留当前 SQLite + Schema v4 + `MemoryService` + 独立遗忘账本的总体架构，不推倒重写，不默认新增 Schema v5，不引入向量数据库、外部摘要模型或主动消息调度器。

本轮按以下顺序交付：

1. **G0：冻结基线与复现。** 先写能证明当前缺陷的失败测试，保留现有正常路径。
2. **G1：关闭写入与审批旁路。** 修墓碑绕过、模型自批、内部治理字段暴露、actor 缺失和秘密分类缺口。
3. **G2：关闭读取与外发旁路。** 修 Seahorse 跨会话读取、后台摘要绕过 egress、不可信聊天身份和调试正文日志。
4. **G3：修复迁移、账本与恢复底座。** 任何维护写入先备份；新库初始化原子化；账本身份与 pending 状态可对账。
5. **G4：完成跨存储遗忘。** 统一正常 Forget 与 Replay 的派生数据清理、JSONL/Seahorse 清理、partial/unavailable 反馈和并发控制。
6. **G5：修检索、域和预算正确性。** 权限与 TTL 在 LIMIT 前生效；不安全的域能力先拒绝；预算语义一致且溢出可观测。
7. **G6：统一 API、CLI、UI 与配置。** 危险操作默认预览，设置更新不再隐式清空安全字段，状态反馈不夸大成功。
8. **G7：建立真实数据质量测量。** 本轮只交付匿名化召回评测、FTS/去重测量；长文分块和时间抽取转入后续独立工作。

G0—G4 是安全与数据完整性阻断项；G5—G6 是正确性和可运维性放行项；G7 仅测量部分可在核心缺陷关闭后并行。语义检索、长文自动拆分、外部 LLM 抽取/摘要、主动回忆均不属于本轮核心交付。

### 0.2 推荐方案已确定

本计划直接采用以下推荐取舍，不再逐项重复设计讨论：

- 模型敏感动作采用**直接拒绝**：模型仅保留受控 add/search/inspect、受限非核心 correct、legacy load_capsule 和 forget preview；confirm、archive/restore、所有 update_state/relationship 写入及任何 force/pinned/boundary 变更返回 owner-only，不让模型创建可执行计划。
- owner change plan 使用现有 `memory_change_plans.payload` 保存带版本、域、请求 ID、白名单参数和批准上下文的短期信封，不为审批元数据新增列。
- 使用现有 `sources.evidence_root` 保存**宿主导出的版本化来源根证据描述**；一个 source 行对应一个唯一 origin event/root，不为完整证据 HMAC 新增列。
- 使用现有 `actor_ref`、`request_id`、`reason_code`、`policy_revision` 完成审计，不新建审计系统。
- 历史 forget 只做只读报告；无 domain 的 content hash 不用于自动删除或批量补写账本。
- 多域 relationship、domain 注入和多域 capsule 在本轮 fail-closed；真正的多域支持另提 RFC，不用不可信上下文拼接实现。
- 已损坏且缺业务、义务或审计状态表的现有库必须故障关闭并引导恢复备份，不能自动创建空表伪装修复成功。
- 旧数据无法证明归属、来源或删除覆盖时保持 `legacy_unassigned`、`unknown`、`partial` 或 `outcome_unknown`，不补造事实。
- 空 `allowed_chat_recipients` 在本轮继续保持兼容放行并明确警示；严格默认拒绝作为独立配置迁移工作，不在安全修复中偷换旧配置语义。
- `budget_tokens=0` 不表示关闭记忆；沿用现有配置归一，删除 egress 的预算短路。只有显式 `enabled=false` 才关闭长期记忆。
- `enabled=false` 不停止已接受的 forget cleanup；会话摘要是否外发按独立用途和接收方策略判断。
- 核心 boundary/pinned 规则不静默截断；普通记忆先裁剪，核心规则溢出必须可见且有硬上限。

### 0.3 非目标

本轮不做：

- 恢复 v1→v2→v3 旧迁移链，或在 `Open` 中自动迁移业务 Schema。
- 新建 service/repository 层、通用任务平台或新微服务。
- 将 MEMORY.md、每日笔记重新引入运行时。
- 为“使用到现有表”而开启承诺、主动召回或外部处理。
- 在本轮拆分每 Agent 独立 Seahorse Engine、开放完整多域、实现长文自动分块或扩展时间抽取。
- 自动认领 legacy 数据、自动合并相似长文或批量修改真实记忆。
- 承诺应用层审批可以阻止拥有同 UID 文件或任意 exec 能力的模型直接读写文件。
- 在没有用户明确发布请求时修改版本号、打 tag、触发 Release 或部署。

## 1. 已核实基线与证据

### 1.1 当前实现中应保留的能力

- 仅接受显式 v3→v4 迁移，旧版和未来版本拒绝。
- 常规受保护读写具有 pending-migration 故障关闭路径。
- 同域精确去重、Correct 版本化、摘要失效、Forget 主库事务与独立账本框架已经存在。
- 主检索 FTS/LIKE 的权限过滤在 LIMIT 前执行，并有回归测试。
- 主聊天、流式、单 Provider 和 fallback 候选已有 egress 检查。
- 账本缺失或损坏时 Forget/Replay 故障关闭。
- 参考主库与账本均通过完整性检查，未发现外键异常或 active 精确重复组。

### 1.2 参考数据库只揭示该快照的事实

| 指标 | 快照结果 | 能得出的结论 | 不能得出的结论 |
|---|---:|---|---|
| entries | 117：110 active、7 superseded | 数据规模较小 | 不能代表线上当前规模 |
| 正文中位数 | 1346 字节 | 主要是长文档型记忆 | 不能据此认定所有长文都应拆分 |
| 正文超过 1KB | 79/110 | 需要摘要、展开和预算治理 | 不能证明检索一定慢 |
| event_at 为空 | 110/110 active | 时间召回缺乏数据 | 不能用 created_at 代替事件时间 |
| use_count=0 | 96/110 | 使用观测不足或召回集中 | 不能直接认定 96 条都是垃圾 |
| domain | 全部为 legacy_unassigned | 多域路径未被真实数据验证 | 不能宣称多域隔离已验收 |
| sources | 117 条，每条记忆一个来源 | 缺少多来源 corroboration | 不能据此伪造额外来源 |
| message_id/origin_event/model/prompt | 全部为空 | 证据链不可完整追溯 | 不能自动补造 |
| actor_ref | 274 条操作均为空 | 操作无法归属可信主体 | 不能倒推历史批准人 |
| 历史 forget | 177 次、影响 459 条 | 存在账本前历史删除义务 | 不能假设都能从现有账本恢复 |
| 当前账本 | 2 条 applied | 仅覆盖最新两次主库阶段 | 不等于外部副本清理完成 |
| cleanup jobs | 2 条 pending，始于 2026-11-22 | 清理积压真实存在 | 不能在未执行时宣称已删除会话副本 |
| 精确重复组 | 0 | 无需大规模精确去重 | 不排除语义重复 |

### 1.3 已执行验证

在代码基线相同的情况下已执行并通过：

```sh
go test -tags 'goolm stdjson' \
  ./pkg/memstore ./pkg/agent ./pkg/tools ./pkg/tools/shared \
  ./pkg/identity ./pkg/session ./pkg/memory ./pkg/seahorse \
  ./web/backend/api ./cmd/picoclaw/internal/memory \
  -count=1 -timeout=5m

CGO_ENABLED=1 go test -race -tags 'goolm stdjson' \
  ./pkg/memstore -count=1 -timeout=5m
```

当时 CLI 记忆子命令没有测试文件。以上结果只说明既有用例通过，不覆盖本计划列出的对抗性、跨存储和恢复场景。

## 2. 优先级、交付门禁与完成定义

### 2.1 优先级

| 级别 | 含义 | 放行规则 |
|---|---|---|
| P0 | 隐私、权限、永久遗忘、不可恢复数据损坏 | 未关闭不得作为“治理已完成”发布 |
| P1 | 可见性正确性、TTL、域、预算、配置与状态真实性 | 核心路径未关闭不得开启多域/多 Agent 扩大使用 |
| P2 | 质量、性能、语义、文档和体验增强 | 不阻塞 P0/P1 安全修复，不得抢占其测试资源 |

### 2.2 每个工作包的完成定义

一个工作包只有同时满足以下条件才算完成：

- 先提交能复现原问题的失败测试，再提交最小修复。
- 所有入口遵循同一权限与治理语义，不能只修 UI 或只修 Store 的一个方法。
- 新增写入不产生无法追踪的敏感副本。
- 失败、超时、崩溃和重试不会把 partial 报成 success。
- 测试断言最终数据库状态、外部副本和副作用，不只检查返回值。
- 不引入新依赖；确需新增时单独获得环境变更批准。
- 文档、API 类型、CLI 输出和 UI 文案与实测行为一致。
- 没有运行某项验证时明确标记“未运行”，不得以静态审查代替。

### 2.3 本轮发布阻断条件

出现任一情况即停止放行：

- 已遗忘正文可以通过任何公开入口重新学习或复活。
- 模型可在私聊中自行确认、强制改 pinned core 或改变核心状态。
- Seahorse 可读取不属于当前授权会话的消息。
- 任一 Provider 调用绕过了适用的外发策略。
- 维护路径先改库后创建备份，或备份失败仍继续修改。
- Forget/Replay 将不可核验清理报告为完成。
- 旧配置因字段删除或部分 PUT 而失去原有安全设置。

## 3. 目标不变量

1. 所有正文变更都经过版本化 Correct；公开 DTO 不接受内部治理绕过字段。
2. 用户确认、核心修改、状态提升和永久遗忘都绑定宿主 actor、批准事件与完整 request ID；entry 操作绑定 expected revision，relationship 不伪造不存在的 revision。
3. 模型参数只能缩小权限，不能扩大 domain、audience、recipient 或会话范围。
4. 每次携带长期记忆或其派生内容的 Provider 调用都按用途和接收方检查；策略拒绝不可重试、不静默改投，普通干净聊天不被误伤。
5. 所有记忆可见性、TTL、domain、realm、audience、layer 条件在候选 LIMIT 前生效；管理与历史查询必须显式选择不同语义。
6. 群聊身份无法确认时不按私聊处理；管理面与 CLI 使用显式管理员上下文。
7. 已接受遗忘义务在独立账本中持久存在，主库回滚或 `memory.enabled=false` 都不会消除或暂停该义务。
8. 正常 Forget 与 Replay 使用相同的摘要删除/脱敏、派生数据处理和 cleanup 入队逻辑。
9. 清理以可信来源定位与根证据验证为准；无法验证即 `unavailable/partial`，崩溃后无法区分已删与从未存在时为 `outcome_unknown`。
10. 任何维护写入都遵循预检、停写或维护互斥、主库与账本备份、事务修改、最终复检。
11. 配置兼容不依赖删除已发布字段；安全列表不被部分更新隐式清空。
12. 日志、指标和测试报告不记录记忆正文、原始查询或秘密。
13. 核心规则不被静默截断；普通记忆不能利用最低检索预算突破总目标。
14. 旧数据的 unknown 不被自动改写成 confirmed、owned、verified 或 done。

## 4. 关键设计决策

### ADR-01：所有公开写入使用白名单 DTO

Web/API 的 Add、Correct、Update、archive/restore、review/resolve、forget、relationship、persona 和 import 全部使用显式白名单 DTO，并通过 `DisallowUnknownFields` 或等价机制拒绝未知字段。不能只修三个 entries 端点而让其他 handler 直接调用 raw Store。

`Force`、`ForceStatus`、`BypassForgetTombstone`、可信确认标志、actor、domain、批准事件、`EvidenceRoot`、`OriginEventID`、`SessionKey` 和 `MessageID` 等身份/根证据字段由宿主派生；客户端不能伪造。管理 API 是 owner 面，但也必须遵守目标域、revision 和批准语义。

### ADR-02：模型敏感动作直接拒绝，owner 计划独立加固

模型工具使用显式 allowlist：add/search/inspect、受限非核心 correct、legacy load_capsule 和 forget preview。correct 只允许非 pinned、非 boundary、同 domain/audience/layer 且无 Force 的修正，并重新检查墓碑；load_capsule 只允许可信 channel 与 legacy/domain 校验通过的场景。

`Purpose=="chat"` 时，confirm、archive/restore、所有 update_state/relationship 写入，以及任何 force/pinned/boundary 变更一律返回 owner-only；本轮不让模型创建可执行 change plan。模型工具写入使用 `SourceInferred`；只有证据确为用户明确陈述时才使用 `SourceExplicit`。

owner 在 dashboard/CLI 的显式确认可以继续使用受控计划或直接批准路径，但必须经过同一 DTO、actor、domain 校验；entry 操作另校验 expected revision。

理由：直接拒绝先用最小改动关闭 P0，避免为了“模型可申请”同时引入 payload 注入、跨域批准和崩溃恢复的新攻击面。

### ADR-03：owner change plan 使用类型化参数与独立请求 ID

计划只允许 owner 入口创建；每个 action 使用独立 typed params，禁止把任意 `AddInput` 或 `RelationshipUpdate` 原样放入 payload。payload 使用版本化信封：

```json
{
  "schema": 1,
  "protected": {
    "request_id": "full-uuid",
    "domain_id": "legacy_unassigned",
    "initiator": "dashboard|cli",
    "params": {}
  },
  "approval": null
}
```

`params_digest` 覆盖规范化 protected 信封；批准时重新计算并校验 action、domain、目标和 expiry，entry 计划另校验 expected revision。claim 的同一原子更新写入 `approved_by` 和 approval actor/time/context；完整 request ID 贯穿 ledger intent、operation 和 cleanup job。plan ID 不再兼任跨表幂等键，但通过 operation reason_code 保留 plan↔request 关联，确保终态清空 payload 后仍可审计。

entry 操作必须绑定 expected revision。当前 relationship 没有 revision，本轮不承诺 relationship plan 的 revision CAS；敏感 relationship 采用 legacy owner 直接批准和显式前置条件，或保持不支持。

状态分为清理 payload 的 `executed/expired/denied/failed_terminal`，以及保留恢复信息的 `executing/failed_retryable/outcome_unknown/blocked`。只有已执行、已过期、已拒绝或确认无未清理副作用的终止失败才能清 payload；executing/retryable/unknown/blocked 保留白名单参数和批准记录。blocked 不得自动执行，只能在修复冲突并生成新预览后由 owner 显式 requeue。发生副作用后结果不确定时不得直接标记 failed。

### ADR-04：evidence_root 保存宿主导出的来源根

`evidence_root` 使用紧凑 JSON，至少包含 `v`、`algo`、`key_id`、`type`、`canonicalization`、`digest`。根类型与规范化版本共同决定输入字节；未知类型、版本或损坏 JSON 一律 unavailable，不能回退到 120-rune 展示文本。

根只能由宿主在可信入站/工具边界生成，覆盖完整来源单元，而不是证据片段。若证据只是长消息中的一部分，根仍对应完整 Content、tool arguments 或 Seahorse part；清理时先定位候选，再对同一完整单元重算并比较。

一个 source 行对应一个唯一 `origin_event_id` 和一个根。同一 entry 的多个消息、Content/Reasoning/工具参数根必须拥有不同 origin event，不能因现有 sources 唯一键与 `INSERT OR IGNORE` 被静默折叠。重复写入同一 origin event 才允许幂等忽略。

SourceLocator 与 manifest 同步升级以携带根类型、版本和 digest；旧 locator 协议只做严格可验证的兼容匹配。JSONL 没有可依赖的内部消息 ID，使用 session + 根扫描；Seahorse 本地 message ID 与平台 message_id 不得混用。

MVP 只覆盖当前真实来源和已知副本：JSONL Content、tool-use arguments、tool result Content，以及 Seahorse content/text/tool-use parts。Reasoning、媒体和其他根类型只有实际接入来源后才启用；共享媒体文件不因消息遗忘而默认删除。

### ADR-05：历史 forget 只读报告，不自动删除

历史 `memory_operations.content_hash` 是无 domain 的无盐内容哈希，不能转换为账本 HMAC，也不能证明删除义务的当前归属。历史对账因此只输出 target ID、时间、可匹配状态、domain 歧义和 locator 完整率，**不自动写 journal、不按 content hash 删除**。

只有在授权恢复副本中 target entry 仍存在、操作时间有效、显式 legacy 维护模式且 owner 再次确认时，才能作为一个**新的维护删除动作**执行并生成新的 actor/request/ledger 记录。不得把这批动作伪造成原历史批准。

当前 Replay 与现代 ledger intent 必须走另一条按 manifest entry IDs + domain 的路径，不能与历史 content-hash 报告混用。当前主库已不存在的 177 次历史 forget 操作不生成噪声 journal；连 operation 也缺失时明确无法恢复。

### ADR-06：现有损坏库不自动补空状态表

全新空库只在**不存在任何已知 memory 业务对象**时判定为 fresh，并在单事务中创建完整 v4；`loadRevision` 不得在初始化前单独补写 schema_version=4。只要已有对象残留而 meta/entries 缺失，就按损坏处理，禁止执行基础建表脚本。

已有 v3 缺少 v4 表属于待迁移，不属于损坏。已有 v4 缺少 `entries`、`sources`、`personas`、relationship 基础表、`memory_operations`、`memory_jobs`、`memory_change_plans`、`memory_origin_events`、`memory_domains` 或独立账本，均视为业务/义务/审计状态不可重建，返回 `non_repairable` 并引导恢复备份。

只有索引、触发器、FTS 和明确可重建探针对象可进入显式 repair。不能用创建空表的方式删除历史墓碑、待审批计划或清理义务。

### ADR-07：清理复用活跃实例与原子会话操作

Gateway 内清理复用各 Agent 的活跃 JSONLStore 与 Seahorse Engine，不另建无共享锁的存储实例。JSONL 在现有 store 内提供 session 级原子 mutate：同一 shard 锁内 flush、读取、验证、重写并返回错误；禁止外部 GetHistory 后再 SetHistory，也不使用吞错的 SessionManager.SetHistory。

CLI 不提供在线并发清理。离线维护采用 workspace 级原子创建维护锁、gateway PID 双检和 gateway 启动反向检查；现有 PID 文件只作为活性信号，不冒充互斥锁。无法证明停写或锁状态异常时拒绝执行，不能用 `--force` 绕过。

### ADR-08：多域能力本轮 fail-closed

当前 `relationship_state(key PRIMARY KEY)` 无法表达不同 domain 的同名 key，检索、capsule 和异步上下文也没有完整可信 domain 传递链。本轮只保证：

- 所有 Store/Service 读写删显式校验 domain，禁止跨域 supersede、Replay、Capsule 和 relationship 覆盖。
- 普通运行时保持显式 legacy 兼容；缺少可信 domain 的非 legacy 请求返回不支持，而不是回落全库。
- 非 legacy relationship 写入返回明确不支持；删除优先依据 source_event_id，只有 legacy 无标记且 predicate 精确匹配 nickname/birthday/anniversary 的副本才允许受限 fallback，其他情况保留并报告 unavailable。
- 管理面全局导出明确标为 owner-only，普通模型不能借管理接口扩大权限。

完整的 domain 下推、多域 relationship、capsule 与每 Agent 上下文另提 RFC。本轮不宣称多域已开放或已验收。

### ADR-09：预算采用目标预算与核心溢出两层语义

- 沿用配置加载和设置 API 已有的 `budget_tokens=0` 默认归一；删除 egress 的预算短路并更新原测试，不重复造一套归一逻辑。
- 普通 profile、episodic、relationship 与 retrieval 在一个总目标预算内裁剪，不叠加多个百分比形成隐性超支。
- 取消检索固定最低 200 的强行注入；固定块已耗尽预算时检索为 0。
- pinned/boundary 不静默截断或丢弃后续规则。其超出目标预算时返回明确 overflow/cut 指标并压低普通检索。
- G0 用真实长度 fixture 冻结核心总量和单条绝对上限；超过上限拒绝装配、给出可操作错误且 Provider 调用为 0。
- token 估算明确是保守估计，不宣称等于每个 Provider 的真实 tokenizer。

### ADR-10：兼容优先，不偷换配置语义

空 recipients 在本轮仍是兼容放行，但在状态页标为 `compatibility_allow_all`。严格 allow-list/deny-all 需新增显式模式和旧配置回归测试后单独交付。`enabled=false` 才是关闭长期记忆的明确方式。

### ADR-11：外发按用途判断，清理不受学习开关控制

无长期记忆的普通主聊天保持原有 Provider 行为；携带长期记忆、`/btw` 侧问、legacy/Seahorse 会话摘要、subagent 转发、外部抽取或 embedding 的调用按用途和实际接收方检查。摘要即使不含长期记忆条目，也属于会话派生数据处理，不能因 `memory.enabled=false` 或 budget=0 绕过已配置 recipients。

策略拒绝保留 `errors.Is(ErrMemoryEgressDenied)` 并终止该调用链，不因拒绝而静默改投 fallback；普通可重试错误仍按原 fallback 机制逐候选重新检查。

遗忘清理是已接受义务，与长期记忆学习开关无关。`memory.enabled=false` 只停止长期记忆装配和学习，不能停止 pending ledger/job 的对账与清理。

## 5. 工作包总览与依赖

| 工作包 | 内容 | 优先级 | 依赖 | 可并行 |
|---|---|---|---|---|
| G0 | 基线、失败测试、临时副本与测量 | P0 | 无 | 不与其他写代码任务并行改同一测试 |
| G1 | 写入、审批、actor、秘密与 DTO | P0 | G0 | G2 的部分只读隔离测试 |
| G2 | Seahorse 会话隔离、上下文身份、egress、日志 | P0 | G0 | 与 G1 并行，避免共享文件冲突 |
| G3 | 迁移、备份、账本身份与恢复对账 | P0 | G0 | 与 G1/G2 并行 |
| G4 | Forget/Replay、根证据、JSONL/Seahorse 清理 | P0 | G1、G3 | JSONL 与 Seahorse 子任务可并行 |
| G5 | 检索、TTL、域限制、预算 | P1 | G1；胶囊部分依赖 G4 域契约 | 两个独立子任务可并行 |
| G6 | API、CLI、UI、配置与状态反馈 | P1 | G1—G5 的契约 | 前端与 CLI 可并行 |
| G7 | 匿名化质量评测与 FTS/去重测量；长文/时间另行立项 | P2 | G5；真实副本验证依赖 G4 | 质量与性能测量可并行 |

依赖不是提交顺序的绝对限制，但合入与验收必须满足依赖。每个工作包保持小提交，一次只处理一个缺陷族。

## 6. G0：冻结基线、复现与测量

### 6.1 范围

- 固定当前 HEAD、上游、工作区 diff 和 Go/Node 工具链。
- 所有数据库实验使用新建临时目录中的合成 fixture 或授权副本。
- 不启动真实 gateway，不修改 `/home/zemi/下载/memory` 原文件。
- 建立缺陷编号、测试映射和发布门禁清单。

### 6.2 必须先出现的失败测试

| 编号 | 复现内容 | 当前预期 |
|---|---|---|
| R-01 | Forget 后以相同正文调用 Correct | 当前可绕过墓碑 |
| R-02 | API AddInput 携带内部 bypass 字段 | 当前被接受 |
| R-03 | 私聊模型 confirm/force/archive/restore | 当前可直达执行 |
| R-04 | 两会话 Seahorse grep/expand | 当前可跨会话读取 |
| R-05 | legacy/Seahorse 摘要 Provider 调用 | 当前无 egress |
| R-06 | 超过 120 runes 的来源消息清理 | 当前不可匹配 |
| R-07 | 同一证据出现在两条消息 | 当前可能只删一条 |
| R-08 | Replay 后摘要与 cleanup job | 当前未统一处理 |
| R-09 | v4 缺对象 repair 备份顺序 | 当前可能先改后备 |
| R-10 | 新库初始化中途崩溃 | 当前可能留下不可修复 v4 |
| R-11 | 超过 40/30 条不可见固定块后存在 public sentinel | 当前可能被挤出 |
| R-12 | Search/1-hop/capsule 中过期条目 | 当前可能仍返回 |
| R-13 | 部分 settings PUT | 当前可能清空安全字段 |
| R-14 | partial/blocked cleanup CLI | 当前可能退出 0 |
| R-15 | 两个进程首次创建同一账本身份 | 当前存在覆盖竞态 |
| R-16 | `/btw` 与 subagent Provider 调用 | 当前存在未覆盖发送点 |
| R-17 | `memory.enabled=false` 时存在 pending cleanup | 当前 worker 直接返回 |
| R-18 | owner plan payload 携带 Force/domain/未知字段 | 当前缺少完整白名单与 digest 校验 |
| R-19 | archive/review/relationship/persona/import 携带未知字段 | 当前 raw Store/普通 JSON 解码可绕过 DTO 闭包 |
| R-20 | 同一 entry 来自两个不同消息根 | 当前 sources 唯一键可能折叠后续来源 |
| R-21 | 后端删除成功但 CompleteJob 前崩溃 | 当前重试可能把已删除报成 unavailable |
| R-22 | 设置缺省 recipients/V4 字段、`memory:null`、显式空数组 | 当前可能清空配置或误判已配置 |
| R-23 | 未知 ChatKind 与群聊异步 follow-up | 当前可能按 private 学习 |
| R-24 | fallback 主候选被策略拒绝 | 必须停止策略拒绝链且 Provider 调用为 0，不静默改投 |
| R-25 | Seahorse Compact/Ingest 与消息清理并发 | 当前锁未覆盖全部路径 |

### 6.3 真实快照的匿名化测量

只输出计数、长度、状态和时间，不输出正文。保存以下基线供后续对比：

- active/status/domain/layer/audience 分布；
- 正文长度分位数、source 定位字段完整率；
- use_count 分布，但不将其等同于相关性；
- job/state/attempts/age 和账本 seq；
- SQLite page、FTS 占用与 freelist；
- 高置信秘密模式仅输出命中计数，不输出上下文。

### 6.4 验收与回滚

验收：上述测试在修复前能按预期失败，且失败原因与目标缺陷一致；测试使用临时目录和可控时钟。

回滚：删除新增测试与测量脚本即可；不得为让初稿测试通过而修改真实数据。

## 7. G1：写入、审批与审计闭环

### G1-01：关闭墓碑与跨域 supersede 旁路

**修改范围：** `pkg/memstore/store.go`、`governance.go`、`service.go`、`types.go` 及对应测试。

**实施：**

1. Correct 对新正文执行与 Add 等价的墓碑检查。
2. 显式 `SupersedesID` 校验目标 domain、状态和调用者权限。
3. `Update` 不再允许正文变更；所有正文变更转 Correct。
4. reassign_audience 计划只接受元数据白名单。
5. `blocked_forget` 在 API 中返回 409 和明确 action，不返回假成功。
6. 如需 owner 恢复已遗忘正文，单独设计带批准事件的 unforget；本轮不默认开放。

**验收：**

- Add、Correct、API、模型工具、导入和计划执行均不能复活已遗忘正文。
- domain A 不能 supersede、correct 或删除 domain B 的条目。
- 旧版本历史仍保留，合法 Correct 行为不变。

### G1-02：模型直接拒绝与 owner 计划状态机

**修改范围：** `pkg/tools/memory.go`、`pkg/memstore/service.go`、`governance.go`、`store.go`。

**实施：**

- 模型动作采用显式 allowlist：add/search/inspect、受限非核心 correct、legacy load_capsule 和 forget preview。correct 仅限非 pinned、非 boundary、同 domain/audience/layer、无 Force 且通过墓碑检查；load_capsule 必须通过可信 channel 与 domain 校验。
- `Purpose=="chat"` 时，confirm、archive/restore、所有 update_state/relationship 写入，以及任何 force/pinned/boundary 变更直接返回 owner-only；本轮不让模型创建执行计划。
- Store 层保留内部能力，MemoryService 对非 admin 上下文拒绝执行审批动作。
- pending_review 不能直接 restore 为 active；pinned core 状态变化需要 owner 批准。
- `ReviewRequired` 对普通模型写入真实生效。
- owner 计划使用 action 专属 typed params 和 `DisallowUnknownFields`；禁止任意 AddInput、Force、domain 或 evidence root 注入。
- 创建与批准都校验 protected digest、domain、目标、action 和 expiry，entry 计划另校验 expected revision；批准人必须有权管理该 domain。
- claim 时原子保存 approved_by、approval actor/time/context 和完整 request ID；request ID 贯穿 operation、ledger 和 cleanup job。
- `executing/outcome_unknown` 按 request ID 查副作用：已有则补终态，无副作用且 entry revision 未变才允许重试；冲突保持 blocked，只有生成新预览后 owner 显式 requeue。
- 只有 executed/expired/denied/failed_terminal 清 payload；retryable/unknown 保留恢复所需的白名单参数和批准记录。
- ledger RecordIntent 在 `BEGIN IMMEDIATE` 中按 request_id 查询并返回相同既有 intent；不依赖新增唯一索引实现幂等。

**验收：**

- 私聊模型与群聊模型都不能自批或创建执行计划。
- payload 篡改、未知字段、跨 domain 和 digest 不匹配均被拒绝。
- 同一 plan 并发批准最多产生一次副作用；重复 request ID 不产生多条 intent。
- 在 claim、底层提交、最终状态写入三个位置注入崩溃，恢复后不会重复执行或误用旧 revision。
- 计划过期后无法执行；终态 payload 不保留正文。

### G1-03：公开 API 使用白名单 DTO

**修改范围：** `web/backend/api/memory.go`、前端 `memory-api.ts`、API 测试。

**实施：**

- 为 Add/Correct/Update、archive/restore、review/resolve、forget、relationship、persona 和 import 定义白名单请求结构，并拒绝未知字段。
- 上述写入口统一经过 MemoryService 的 owner 上下文，不在 handler 中绕过服务直接调用 raw Store。
- domain、actor、confirmation、force、evidence root、origin event、session/message 身份和内部状态由宿主派生。
- 管理 API 明确是 owner 面，不以普通模型请求上下文自动获得管理权限。
- 只对 export、forget、plan 批准等敏感操作写审计元数据；inspect/capsule 不为读审计新增持久写入。

**验收：** 所有写端点遇到内部或未知字段均返回 400；旧前端仅使用公开字段时行为兼容；任何 handler 都不能绕过 service 审批。

### G1-04：可信 actor 与批准事件

**实施：**

- 从 dashboard session、CLI owner 或 gateway 请求上下文生成 actor，不使用模型自报身份。
- 当前 dashboard 是共享密码 owner 面，没有可区分的人类 principal。使用 `dashboard|cli|gateway@<installation-ledger-uuid>` 表示“该安装实例上的入口 actor”，不得伪称为已验证的个人身份；未来多用户再接入真实 principal。
- Confirm/Correct/Forget/Replay/SetStatus/relationship 全部写 actor_ref、完整 request_id、reason_code、policy_revision。
- 账本 intent 的现有 scope_json 保存 plan/approval/actor/domain/expected_revision 绑定；没有批准绑定的 pending intent 只能报告，不能自动 replay。
- operation log 与 ledger/cleanup 通过完整 request ID 关联，批准人不再统一写死 `dashboard`。

**验收：** 新操作 actor 非空且与宿主入口和安装身份一致；历史空 actor 保持 unknown，不批量补写；无批准绑定的 intent 不自动执行。

### G1-05：秘密、敏感信息与非可信指令边界

**实施：**

- 扩展高置信秘密检测：password、passwd、bearer、常见 token 与私钥格式。
- 所有写入，包括 update_state、relationship、自动抽取和 Correct，统一分类与拒绝。
- 自动抽取对健康、住址等只提升保护级别，不降低调用者已给级别。
- relationship update_state 设置固定长度上限，超限拒绝而非静默截断。
- 模型工具不能写 boundary；记忆正文即使位于 system 段也标记为不可信数据，不授予工具或网络权限。

**验收：** 中英文秘密、tool arguments、relationship 和长输入均有负例；普通相似文本不过度拦截。

### G1 回滚

按独立提交回滚 DTO、工具动作或分类器改动。回滚不得重新开放已知 bypass 作为默认兼容方式；如必须暂缓发布，应保留危险动作禁用状态。回滚前必须处理 pending/executing plan：完成幂等对账或使其过期并清除 payload，不能让旧二进制继续执行新协议计划。

## 8. G2：读取隔离、上下文身份与外发

### G2-01：Seahorse 会话与 Agent 隔离

**修改范围：** `pkg/seahorse/tool_grep.go`、`tool_expand.go`、`short_retrieval.go`、`store.go`、`pkg/agent/context_seahorse.go`。

**实施：**

1. 从可信工具上下文取得 AgentID 与 ToolSessionKey，通过现有 Engine/Store 解析 conversation；缺少可信身份或映射歧义时拒绝。
2. GrepInput/SearchInput 增加可信 ConversationID，默认 `all_conversations=false` 必须实际添加过滤。
3. 模型传入 `all_conversations=true` 直接报错；owner 跨会话能力只走独立管理面，不为模型伪造 admin 上下文。
4. ExpandMessages 在存储查询中加入授权 conversation，不能取出后相信模型提供的全局 ID。
5. 本轮保留单 Engine 架构，以 AgentID + sessionKey + conversation 做强制隔离；sessionKey 冲突或无法证明 Agent 归属时 fail-closed。每 Agent 独立 Engine 与 routed bootstrap 改造另行评估。

**验收：** 两个会话、两个 Agent、群聊与私聊交叉测试均无法读取未授权内容；模型 all_conversations 被拒绝；正常当前会话 grep/expand 不回归。

### G2-02：ChatKind、realm 与异步来源

**实施：**

- 有聊天 channel 但 ChatKind 缺失/未知时，chatKindAllowsPrivate、MemoryContext.AllowsPrivate、audienceSQLFor 和 sharedChatKind 统一 fail-closed。
- 异步 follow-up 至少继承原 ChatType、Channel、ChatID 和 SessionKey；当前 bus 没有可信 domain/source 字段时，禁止该 follow-up 自动抽取或写入长期记忆，不伪造继承。
- 显式 realm 参数仍受 group/public realm 政策约束。
- 管理查询与普通域感知查询保持不同入口，不能互相借用。

**验收：** 群聊工具输出不会被当成私聊证据；未知渠道上下文不读取 private；缺 domain/source 的异步消息只降级处理，不学习；显式 fictional/work 不扩大群聊权限。

### G2-03：所有 Provider 调用统一 egress

**实施：**

- 将现有 `checkMemoryEgress` 抽为接收配置与用途的内部 helper，不修改 Provider 公共接口；Seahorse CompleteFn 按当前调用解析 Agent/session 的 Provider/model，不能长期使用 default Agent 的旧接收方。
- 发送点至少覆盖主调用、fallback、重试、流式、legacy/Seahorse 摘要、`/btw`（`turn_coord.go`）和 subagent legacy tool loop。
- 无长期记忆的普通主聊天保持原行为；携带长期记忆或其派生内容的调用、会话摘要和无法证明干净的 subagent 转发按用途检查。
- 会话摘要即使不含长期记忆条目，也按会话派生数据用途检查 recipients；`memory.enabled=false` 与 budget=0 都不能跳过。
- 策略拒绝返回不可重试 `ErrMemoryEgressDenied` 并终止该调用链，不静默改投 fallback；后台摘要使用明确的本地降级，不复用旧外发快照。
- 普通可重试错误仍可按原 fallback 机制处理，但每个实际候选都重新检查。

**验收：** 每个发送点分别使用允许/拒绝 recipient；拒绝时该候选 Provider 调用严格为 0，策略拒绝后的 fallback 调用也为 0；`errors.Is` 保留；覆盖 enabled=false、budget=0、空 allow-list、`/btw` 和 subagent。

### G2-04：日志去正文

**实施：** 删除 DEBUG 中完整 `messages_json`、工具结果正文和记忆文本；保留 request ID、entry ID、计数、recipient、reason code 与指纹。

**验收：** 对含唯一哨兵正文的请求执行所有日志级别，日志中不出现该哨兵；指纹不能逆推出正文。

### G2 回滚

若统一助手产生兼容问题，可回退到各调用点显式检查，但不能直接恢复无检查的后台摘要。Seahorse 隔离修复回滚时应先禁用相关工具，而不是恢复全库读取。

## 9. G3：迁移、备份、账本与恢复

### G3-01：完整性计划与原子初始化

**实施：**

- 预检覆盖基础表、v4 表、必要列、唯一索引和 FTS 触发器，并先按 meta.schema_version 区分“合法 v3 待迁移”与“v4 损坏”。
- fresh 判定扫描全部已知 memory 对象；只有完全不存在业务对象时才允许初始化。任何残留对象加缺失 meta/entries 都按损坏处理。
- 不可重建清单包含业务表、memory_operations、memory_jobs、memory_change_plans、memory_origin_events、memory_domains 和独立账本；缺失返回 non_repairable。
- 新库基础 Schema、v4 对象、默认域和版本号在一个事务中完成，最后写 schema_version；loadRevision 不再提前补写版本。
- `migrateSchemaLocked`、`ApplyMigration`、`RepairData` 均纳入同一预检与备份协议；每次维护后重新运行完整性计划，未完整不得清 pending。

**验收：** 逐表/逐索引缺失测试；meta/entries 被删但其他对象残留时不得判 fresh；初始化在每个 DDL 点故障注入后，下一次 Open 要么完整可用，要么明确可恢复，不出现假 v4。

### G3-02：任何维护写入前先备份

**实施：**

1. 只读预检并进入维护写锁；无法证明停写时不得继续。
2. 依次生成主库 `VACUUM INTO` 与独立账本一致性快照，记录 main revision、ledger UUID/keyID/max seq/pending count。
3. 校验备份可打开、完整性、账本身份与 seq；含 pending intent 的备份标记为恢复后需对账，而非静态完成态。
4. 执行单事务修改。
5. 最终复检并返回 backup manifest；解除维护锁后，新义务由最新账本继续承载。

有需要变更时必须创建新备份；幂等无变更返回 `no_changes`，不谎称重新备份。`last_migration_backup` 不得仅因字段非空就复用一个可能已不存在的文件。v4 repair 也必须先备份，ApplyMigration/RepairData 不得先调用 migrateSchema 再补备份。

**验收：** 备份失败时数据库零变更；主库 revision 与账本 max seq/pending 写入 manifest；第二次幂等执行零数据变更；UI/CLI 显示真实 backup/no_changes。

### G3-03：账本身份、freshness 与 pending 对账

**实施：**

- 身份创建使用 `BEGIN IMMEDIATE`、事务内二次读取、条件插入和提交后复核。
- RecordIntent 同样在立即事务中按完整 request_id 查询：相同 intent 返回既有行，不同内容复用 request_id 返回冲突；不以新增唯一索引作为本轮前提。
- VerifyLedgerBackup 保留身份匹配语义，另输出 backup_max_seq、active_max_seq、max_applied_seq、pending_count 和 latest_digest；stale 只表示水位差异，不等同身份损坏。
- 恢复时选择同身份中最新有效账本，绝不回滚最新账本，也不允许备份账本覆盖活动账本的更高 seq。
- intent 的 scope_json 必须绑定 plan/actor/approval/domain/expected_revision；缺失绑定的历史 pending 只能报告。
- pending intent 对账在后台维护 worker 中执行，不在 Open 中自动改业务状态：
  - 主库存在同 request_id 操作时，只重试 MarkApplied，不重复删除；
  - 无操作且批准绑定完整时，按 manifest entry IDs + domain 走统一管线，revision 冲突则 blocked；
  - 无法判断时保持 pending/blocked 并报告；blocked→pending 只能显式 requeue。
- cleanup job ID、manifest 和 intent request ID 必须一致；不能只凭 job 存在就认定主库副作用完成。

**验收：** 双进程首次 Open 不产生两个身份；重复 request ID 不产生重复 intent；MarkApplied 失败、主事务失败、无批准 intent、revision 冲突和崩溃点均有恢复测试。

### G3-04：历史 forget 对账与恢复矩阵

**实施：**

- 提供显式 dry-run 对账入口，输出 target_entry_id、时间、可匹配状态、domain 未知/歧义、locator 完整率；不自动写 ledger、不自动删除。
- 历史 operation 只读扫描。只有恢复副本中 target entry 仍存在、显式 legacy 维护且 owner 再确认时，才作为新的维护删除动作处理。
- 现代 Replay 与历史报告分离：Replay 按每个 intent 的 manifest entry IDs + domain 删除，不再全库按 digest 扫描。
- 读取全部适用 manifest，不使用最近 500 条作为静默上限；分页处理并报告总数。

**恢复矩阵：**

| 场景 | 预期 |
|---|---|
| 旧主库 + 最新有效账本 | 已记录遗忘重新执行，派生清理重新入队 |
| 新主库 + 旧账本 | 保留主库状态并提示账本陈旧，不覆盖最新账本 |
| 主库 + 不同身份账本 | 故障关闭 |
| 主库 + 损坏账本 | Forget/Replay 故障关闭 |
| 历史 forget 有 operation、无 ledger | 只生成候选报告；owner 新批准的维护动作另行记录，外部清理不可证明则 partial |
| 历史 forget 连 operation 也缺失 | 明确无法恢复义务，不宣称完整 |
| 同正文存在于两个 domain | 现代 Replay 只删除 intent 指定 domain/entry；历史 hash 不用于自动删除 |
| 超过 500 条 intent | 全量分页处理，无静默遗漏 |

### G3 回滚

代码回滚前必须保留最新主库与账本。任何二进制回滚都先验证其理解的 Schema 和删除协议；不承诺旧二进制可直接操作新数据。

## 10. G4：跨存储遗忘与清理闭环

### G4-01：统一 Forget/Replay 管线

**实施：**

- 抽取共享的内部事务步骤：目标校验、条目删除、关系处理、摘要处理、operation、cleanup 入队、revision。
- 独立账本先写 pending；主库事务提交后才 MarkApplied。两个 SQLite 库没有共同原子事务，不得把 MarkApplied 写成主库事务的一部分。
- Replay 使用相同步骤，按每个 intent 的 manifest entry IDs + domain 定位；不再全库按 digest 匹配，也不使用 Recent(500) 静默上限。
- 所有实际 Forget/Replay 都校验 expected revision；目标已变化时冲突并要求重新批准。
- relationship 优先按 source_event_id 删除可证明副本；仅 legacy 无标记且 predicate 精确匹配 nickname/birthday/anniversary 时允许受限 fallback，其他手工状态、跨域同 key 或无法证明归属的值保留并报告 unavailable。
- 所有失败都保留独立账本义务，不因主库 AlreadyGone 宣称所有副本完成。

**验收：** 正常 Forget 与恢复 Replay 得到相同最终副作用；重复 request ID 幂等；跨域同文不误删；目标 revision 变化不会被旧批准删除。

### G4-02：摘要与派生数据处理

**实施：**

- 对包含目标 entry 的 memstore 摘要版本清除或重写 `summary` 与 `claims`，保留不含正文的 invalidated 元数据。
- 删除对应 summary evidence、evidence span 和 dependencies。
- 无法精确证明历史摘要包含关系时，保守删除/脱敏同 cluster 的可派生活动摘要，并将无法核验部分计入 partial。
- 从剩余成员重建摘要前重新检查 domain、generation 和成员 revision。
- SQLite 事务内只做删除/脱敏与持久 rebuild 任务；任何 LLM 重建都在事务外执行，重建失败不得恢复旧正文。

**验收：** 数据库查询不再返回已遗忘正文；ListSummaries/API 不能读取 invalidated 正文；重建摘要不包含被删 claim；事务内不发生 Provider 调用。

### G4-03：来源根证据与 locator

**实施：**

1. 在可信入站、自动抽取和工具调用边界生成 origin event；公开 DTO 不接受客户端提供的根、origin、session 或 message 身份。
2. 每个来源单元使用唯一 origin event。一个 entry 来自多个消息或多种根时，每个 source 行保存不同 origin/root；同一 origin 重试才幂等忽略。
3. 按实际来源类型计算完整 JSONL Content、tool-use arguments、tool result Content 或 Seahorse content/text/tool-use part 的根 digest，写入版本化 evidence_root。
4. SourceLocator 与 manifest 同步携带 root 类型、canonicalization、key ID 和 digest；`sourceLocatorsLocked` 不再从 120-rune evidence_text 生成新根。
5. 旧记录没有根证据时，只执行严格可验证的旧协议匹配；未知版本、损坏根和无法证明的片段均报告 unavailable。
6. sourced write 需要根证据但账本身份不可用时拒绝并说明：这意味着相关自动抽取写入暂停，是明确的故障模式，不写成局部成功。无来源手工记录可保存，但标记不可定位。
7. JSONL 使用 session + 根扫描；Seahorse 新 ingest 记录 session→conversation/local message 映射，存量无映射时只在可证明 conversation 的范围内扫描，否则 unavailable。

**验收：** 长消息中的片段证据、完整长消息、同 entry 多来源、工具参数、tool result 和 Seahorse parts 都有测试；来源不会被唯一键静默折叠；数据库和日志不出现根正文。

### G4-04：JSONL cleaner

**实施：**

- 在现有 `pkg/memory.JSONLStore` 内提供 session 级原子 mutate：同一 shard 锁内 flush、读取、验证、重写并返回错误；Gateway 从各 Agent 的活跃后端取得该能力。
- 禁止外部 GetHistory→SetHistory 和使用吞错的 SessionManager.SetHistory；无法提供原子能力的后端保持 blocked。
- 删除所有经过验证的重复匹配，而不是每个 digest 只删第一条。
- MVP 支持 Content、结构化 tool-use arguments 和 tool result Content；不尝试对任意 JSON 子串做不可靠 HMAC，也不虚构 JSONL 内部消息 ID。
- 每个 locator 返回 removed/already_absent/unavailable/outcome_unknown 和原因码，不输出正文。
- 在现有 job payload 中持久化每后端、每 locator 回执。后端已提交但回执未持久化时，重试不能证明“已删除”，必须报 outcome_unknown，而非直接 already_absent 或永久假完成。

**验收：** 同会话重复消息、跨会话同文、长文本、工具参数、tool result、并发 append、回执前后崩溃和迟到写入均有测试；不得丢失未命中消息，不得把找不到直接当作已删除。

### G4-05：Seahorse cleaner

**实施：**

- 在 `pkg/seahorse` 新增按授权 conversation、message/part 与根证据验证的事务删除方法；平台 message_id 不冒充 Seahorse 本地 ID。
- Ingest、Compact、CompactUntilUnder、ClearSession、异步 condensed compaction 与新增清理共用同一 per-conversation 锁，并配合 generation/tombstone 防止迟到回写。
- 一个事务内处理 messages、parts、summary_messages、context_items 和受影响 summaries，重排必要 ordinal。
- 当前 summaries 没有 invalidated 字段：采用删除或内容脱敏，不写虚假的失效状态。事务内同时持久化 rebuild 任务或标记后续 compaction 懒重建。
- LLM 重建只在事务外执行；失败保持旧摘要已删除/脱敏并报告 partial，不能留原文。
- 不调用整会话 ClearSession 代替精确遗忘，除非 owner 明确选择整会话删除。

**验收：** 遗忘后 short_grep、short_expand、上下文装配和摘要均不能返回目标内容；同会话其他消息保留；Ingest/Compact/Clear/清理并发和重建失败均有测试。

### G4-06：后台任务、重试与反馈

**实施：**

- invalid manifest、cleaner error、无 locator、部分匹配都增加 Unavailable/Details 或结构化原因。
- job payload 保存每后端/每 locator 回执，使 JSONL 已完成而 Seahorse 失败时只重试失败后端；job 总体不是简单 done/blocked 二态。
- blocked job 提供 dry-run 诊断与显式 requeue；只有修复定位或 owner 明确接受 partial 后才能重试。
- ForgetPreview 增加 locator 数、不可定位数、cleanup job ID 和 request ID。
- API/UI 区分 `accepted`、`partial`、`blocked`、`outcome_unknown`、`done`、`unavailable`；done 只覆盖已声明后端。
- CLI 在 partial/blocked/outcome_unknown 时返回非零；removed=0 不单独代表失败或成功。
- cleanup worker 与 `memory.enabled` 解耦；关闭长期记忆后仍执行已接受义务。

**验收：** 参考库形态的 2 条 pending job 在临时副本上能被展示、诊断和重试；单后端成功/单后端失败可独立恢复；无证据或回执不明时永不显示全部完成；enabled=false 不停止清理。

### G4 回滚

清理器上线前先以 dry-run 运行并比较将删 locator/root 集合；Seahorse 另比较本地消息 ID，JSONL 不虚构消息 ID。真实执行前建立主库、账本、JSONL 与 Seahorse 的一致性恢复点。若清理错误，停止 worker 并从恢复点重建受影响后端；不得用旧账本覆盖最新账本。

## 11. G5：检索、域、TTL 与预算

### G5-01：统一可见性条件

**实施：**

- profile、boundary、Search、FTS、LIKE、时间通道、1-hop、capsule 使用共享的 SQL 条件构造；List 与固定块不再只接受无法表达 audience/TTL 的宽泛参数。
- status/domain/realm/audience/layer/TTL 均在 LIMIT 前生效。普通 retrieval 默认排除过期；管理/历史查询只有显式 include_expired 才能读取。
- 为 BuildFixedContextFor、BuildRetrievalContextFor、LoadCapsuleFor 明确新增过滤上下文或提供安全 legacy wrapper，不能只改 SQL 而不修调用方。
- FTS/LIKE 不再硬编码 active，管理查询按请求 status 过滤。
- 1-hop 继承主检索 layer 与 TTL 约束，共享 ID 集合、配额和最终预算。

**验收：** 大量不可见候选不挤掉 public sentinel；pending/archived 管理检索不漏旧条目；过期条目在所有普通路径不可见，管理显式历史查询语义清晰。

### G5-02：域与 capsule

**实施：**

- 本轮普通运行时保持显式 legacy wrapper；缺少可信 domain 的非 legacy fixed/retrieval/capsule/relationship 请求 fail-closed，不从模型参数补 domain。
- 空 domain 不升级成管理员；普通请求默认 legacy 仅用于兼容未认领数据。
- Store/Service 在 cluster 与 member、entry 和 relationship 各层拒绝跨域读取、覆盖和删除。
- 多域 relationship 未改造前拒绝非兼容写入；管理面全局导出明确标注 owner-only。
- 完整可信 domain 从 bus→agent→tool→retrieval 的传递与多域开放另提 RFC，不作为本轮隐式交付。

**验收：** domain A/B 的同文、同 key、同 cluster ID 场景无法互相读取、覆盖或删除；不可信非 legacy 请求明确失败；legacy 行为保持可用。

### G5-03：预算与长输入

**实施：**

- 沿用配置加载与设置 API 的预算归一，并让 `WithLongTermMemory` 复用同一语义；删除 egress 的 budget 短路及原错误测试预期。
- 使用一个总目标预算，不让固定块 45%、boundary 30% 与 base 各自吃满形成叠加超支；固定块耗尽预算时 retrieval=0。
- 普通长文按最终渲染文本计费，包括分隔符和 1-hop。
- 核心规则不得静默丢弃后续条目；产生显式 overflow/cut，超过 G0 冻结的核心总量或单条绝对上限时返回错误且不调用 Provider。
- relationship value 和工具 search 结果设置上限，空 query 的管理枚举不进入普通模型工具。
- `EstimateSystemTokens` 使用实际装配结果，不再直接加完整 BudgetTokens 冒充真实用量。

**验收：** budget=0、单条 7.6KB boundary、多个长块、1-hop 和混合中英文 fixture；断言最终渲染估算、溢出原因和 Provider 是否被调用。

### G5 回滚

可通过配置暂时关闭新检索通道或长文展开，但不能关闭权限/TTL 检查。预算修复回滚不得恢复“0=注入但跳过 egress”的组合。

## 12. G6：API、CLI、UI 与配置

### G6-01：危险操作契约

- API forget 默认 dry-run；dashboard owner 真实执行要求显式 `confirm=true`、expected_revision、宿主 actor 与现有 CSRF/管理认证，不强制为每次 owner 删除再套一层计划。
- 模型 forget 始终只能 preview；owner 计划用于需要延迟批准的动作，不能由模型注入参数。
- CLI forget 继续默认预览，`--yes` 代表 owner 批准并写 actor/request/revision 审计。
- Replay 增加 dry-run/yes，预览 entries、关系、摘要和各后端清理影响。
- AlreadyGone、blocked_forget、accepted、partial、outcome_unknown、done 使用不同状态，不以 HTTP 200 一概而论。

### G6-02：设置更新与配置兼容

- 设置端点采用字段存在性 merge；可保留 PUT 路径兼容旧前端，但解码必须能区分 absent、显式 false 和显式空数组，不能只把 struct PUT 改名为 PATCH。
- `allowed_chat_recipients`、private exclusions、enabled、extract_model、summary/LLM extraction/semantic/proactive/tool forwarding/sensitivity capture 等所有已发布字段缺省时保持原值。
- 同步补齐前端 MemorySettings 类型；同时核对 `/api/config` 全量更新是否间接受清空 memory 安全字段。
- 保留所有已发布 JSON 字段；废弃字段采用 Deprecated + 清空/告警 + 兼容测试。
- `ledger_path` 本轮明确标为 unsupported/deprecated，不在缺少身份迁移协议时接线，也不提供假开关。
- `"memory":{"db_path":...}` 与 `"memory":null` 都有明确兼容语义；部分配置保留安全默认，不因原始子串判断而静默关闭。
- V4 未接线开关在 UI 中显示真实状态，不提供假开关。

### G6-03：状态页与 CLI 可运维性

- 区分 ledger pending intent 与 cleanup job backlog。
- 展示 job state、attempts、next_run_at、age、结构化错误和 requeue 入口。
- 将 `NeedsDataMigration` 拆为 schema pending 与独立数据质量提示；空 predicate 不再造成永久迁移提示。
- 文案准确描述备份、no_changes、partial 和 cleanup 覆盖范围。
- audit/render/capsule 的 limit、channel、budget 与排除条件明确展示，不使用静默截断冒充全量。

### G6-04：测试

- 为 `cmd/picoclaw/internal/memory` 增加表驱动命令测试。
- API 增加 forget、correct、pinned、plans、ledger/replay、jobs、cleanup、blocked add、设置保留和二次迁移测试。
- 前端本轮不强制引入新测试框架；必须执行 lint/build 和人工 UI smoke。若引入框架，作为单独环境变更批准。

### G6 回滚

API 契约变更保留明确错误和迁移说明；不得通过恢复默认硬删除来兼容旧脚本。默认 dry-run 后，旧脚本可能静默变成只预览，必须在响应、CLI 和迁移指南中显著提示 `executed=false`，不能仅依赖 HTTP 200。前端可回滚展示层，但后端仍必须返回真实 partial。

## 13. G7：真实数据质量测量与后续候选项

本轮 G7 只承诺 G7-03/G7-04 的测量与报告。G7-01/G7-02 是后续独立候选项，不属于本轮完成定义，不阻塞 G0—G6 安全整改。

### G7-01：后续候选——长文档摘要与展开

后续若单独立项，优先复用现有 clusters、cluster members、summary versions 和 summary evidence：长文按结构分块、默认注入短摘要、按需展开完整块，并保留成员 revision、generation 和证据。外部摘要模型默认关闭；不对现有 110 条 active 自动批量拆分，先在小样本中盲测。

### G7-02：后续候选——时间与来源质量

后续再评估事件时间、相对日期、区间与时区抽取。created_at 不等于 event_at；无法判断时保持 unknown。新写入只记录真实存在的 origin、model、prompt、actor 和根证据；本地规则没有外部模型时必须留空，不能为“字段完整”填假值。旧记录不生成虚假时间、来源或确认。

### G7-03：召回质量评测

建立 owner 授权、受限保存的评测集：

- 覆盖真实/虚构/work、私聊/群聊、短查询/长查询、时间查询、垃圾查询和否定冲突。
- 每个用例记录期望 entry ID、禁止 entry ID、domain、audience 和是否应展开。
- 私密正文与查询不提交仓库；仓库仅放合成 fixture。
- 报告命中率、禁止命中、最终 token、展开率、延迟和 partial，不以非空结果或 Add 次数作为质量。

96 条零使用记录只作为观测线索，不自动删除或归档。

### G7-04：FTS 与去重测量

- 对 trigram、LIKE fallback 和候选 tokenizer 做同一查询集对比。
- 记录真实库存量、索引大小、延迟、命中和禁止命中。
- freelist=0 时不把 VACUUM 当作索引缩小方案。
- 近重合并增加否定/冲突负例；无证据时不做大规模自动合并。
- 语义检索只有在盲测证明收益后才评估，优先本地方案并重新检查删除同步。

### G7 验收

质量报告必须同时包含正例、负例、权限、时效和成本。任何“准确率提升”都要注明样本量、数据版本和是否使用真实存量。

## 14. API 与状态契约摘要

### 14.1 Forget 结果

推荐结果结构，字段可在实现时按现有风格调整：

```json
{
  "status": "accepted|partial|blocked|outcome_unknown|done|already_gone",
  "request_id": "...",
  "entry_id": "...",
  "expected_revision": 3,
  "ledger_seq": 12,
  "cleanup_job_id": "forget:...",
  "locators": 2,
  "unavailable": 0,
  "backends": {
    "jsonl": "done|partial|blocked|outcome_unknown|not_applicable",
    "seahorse": "done|partial|blocked|outcome_unknown|not_applicable"
  },
  "details": []
}
```

`accepted` 仅表示主库阶段接受；`done` 仅在所有已声明后端均完成后使用；`outcome_unknown` 表示后端与回执之间发生崩溃，无法证明目标已删或从未存在。无法覆盖的备份、导出、共享媒体或未知副本必须明确列入边界，不能用 done 暗示物理世界全部清除。

### 14.2 CleanupOutcome

至少包含：

- backend；
- removed、already_absent、unavailable、outcome_unknown、sessions/conversations；
- blocked、retryable；
- request/job ID 与每 locator 持久回执；
- 结构化 reason code；
- 不含正文的 details。

### 14.3 操作审计

每个新操作至少可关联：actor、完整 request、plan/approval、target、domain、reason、policy revision、ledger seq 和 cleanup job。entry 操作还关联 expected revision；relationship 使用真实存在的前置条件，不伪造 revision。审计只保存元数据，不保存秘密正文。

## 15. 配置与兼容矩阵

| 场景 | 必须行为 |
|---|---|
| 旧配置含 deprecated private_channels | 仍可加载、清空并一次性告警 |
| 只有 memory.db_path 的部分配置 | 保留 enabled 与安全默认，不静默关闭 |
| memory:null | 明确按兼容默认处理并加回归测试，不按原始子串误判 |
| settings 缺省 recipients/V4 字段 | 保持原值；显式 false/空数组才执行变更 |
| recipients 显式为空且为兼容模式 | 保持兼容放行，状态页清楚显示 |
| enabled=false | 停止长期记忆装配/学习；摘要 egress 独立判断；已接受 cleanup 继续 |
| sourced write 需要根但账本不可用 | 暂停相关写入并明确报警，不生成无根来源 |
| budget_tokens=0 | 沿用归一默认值；不跳过 egress |
| 未接线 V4 开关 | UI/API 显示未支持或不暴露，不能假装生效 |
| 新二进制读旧 v3 | 继续故障关闭，要求显式迁移 |
| 缺业务基础表的现有库 | 故障关闭并引导恢复，不自动补空表 |
| 缺派生表/索引 | 显式备份后 repair，最终复检 |

每次配置字段变更都增加“上一版本写出的 config.json 仍可加载”的回归测试。

## 16. 验证计划

### 16.1 单元与契约测试

按修改范围运行：

```sh
go test -tags 'goolm stdjson' ./pkg/memstore -count=1 -timeout=120s
go test -tags 'goolm stdjson' ./pkg/agent ./pkg/tools ./pkg/tools/shared -count=1 -timeout=5m
go test -tags 'goolm stdjson' ./pkg/memory ./pkg/session ./pkg/seahorse -count=1 -timeout=5m
go test -tags 'goolm stdjson' ./web/backend/api ./cmd/picoclaw/internal/memory -count=1 -timeout=5m
go test -tags 'goolm stdjson' ./pkg/config -count=1 -timeout=5m
```

### 16.2 并发与故障注入

```sh
CGO_ENABLED=1 go test -race -tags 'goolm stdjson' \
  ./pkg/memstore ./pkg/memory ./pkg/seahorse ./pkg/agent \
  -count=1 -timeout=10m
```

必须覆盖：

- 双进程账本身份创建与重复 request ID；
- 同 session append 与 cleanup、回执前后崩溃；
- Seahorse Ingest/Compact/Clear/condensed compaction 与清理；
- plan payload 注入、domain/digest/revision 校验、claim 与重复批准；
- Forget 主库提交后 MarkApplied 失败、无批准 intent 和 revision 冲突；
- `/btw`、subagent、摘要、流式、重试和 fallback 的 egress 矩阵；
- `memory.enabled=false` 时 pending cleanup 继续执行；
- 配置 absent/false/空数组、`memory:null` 和旧配置 round-trip；
- 备份失败、磁盘满、任务租约过期和重试耗尽。

### 16.3 前端

在 `web/frontend`：

```sh
pnpm run lint
pnpm run build
pnpm run format
```

需要更新后端嵌入资源时单独执行 `pnpm run build:backend`，并确认生成资产是否应提交。普通 build 不等于更新 backend dist。

### 16.4 真实恢复演练

只在新建临时目录中的副本执行：

1. 记录主库和账本哈希、revision、seq、entry/job 计数。
2. 创建主库与账本恢复点。
3. 依次演练正常 Forget、主库回滚、Replay、partial、重复 Replay。
4. 检查主库、JSONL、Seahorse、摘要和任务状态。
5. 验证未知来源仍报告 unavailable。
6. 比较最终计数与目标 ID 集合，不只比较 removed 数字。
7. 原参考目录在演练前后哈希一致。

### 16.5 文档与发布验证

文档改动运行：

```sh
git --no-pager diff --check
make lint-docs
```

若用户另行要求发布，再按仓库发布流程执行完整构建、前端检查、中文发布说明、CHANGELOG、annotated tag 和手动 workflow。本计划本身不触发这些步骤。

## 17. 可观测性与隐私

只记录：

- request/job/plan/entry ID；
- domain、backend、status、reason code；
- locator/removed/unavailable 数量；
- job age、attempts、lease 和延迟；
- recipient provider/model 与策略结果；
- 内容指纹的不可逆版本化形式（确有需要时）。

禁止记录：

- 记忆正文、原始聊天、完整工具参数；
- 原始查询、摘要正文和 evidence span；
- HMAC key、token、密码和配置秘密；
- 可直接用于拼接私密内容的详细上下文。

Dashboard 的指标不能将 `done` 作为唯一健康标准；必须同时显示 unavailable、blocked 和最老 pending 年龄。

## 18. 回滚总方案

### 18.1 代码回滚

- 每个缺陷族独立提交，避免一次巨型治理提交。
- 安全开关回滚时默认禁用危险能力，不重新开放 bypass。
- Provider、Seahorse、检索等公共签名变更先评估调用方；不得为回滚删除已发布配置字段。

### 18.2 数据回滚

- 主库与账本是两个独立恢复对象，没有共同原子快照。
- 恢复旧主库必须使用最新有效账本并重放义务。
- 不允许用备份中的旧账本覆盖当前账本。
- JSONL/Seahorse 清理前的恢复点只能用于授权演练或明确恢复操作。
- 如果恢复会重新引入已遗忘内容，先停写并执行 Replay/Cleanup，再开放服务。

### 18.3 发布回滚

本计划不授权发布。后续若发布，必须记录二进制、Schema、配置、账本协议和清理协议的兼容矩阵，不能仅回退二进制。

## 19. 风险登记

| 风险 | 概率/影响 | 缓解与门禁 |
|---|---|---|
| 修复只覆盖 Store，模型/API 仍有旁路 | 高/高 | 四层对抗测试；公开 DTO；服务审批 |
| 长证据改为根 HMAC 后仍无法定位片段来源 | 中/高 | 入站保存完整来源根与稳定消息身份；旧数据 partial |
| 清理误删同文但不同域/会话消息 | 中/高 | backend + domain + session + origin + root；Seahorse 另校验本地 ID |
| 清理与 append/compaction 竞争 | 中/高 | 活跃实例、会话锁、generation/tombstone、race 和崩溃测试 |
| 后端已删但回执未写入导致假完成/重删 | 中/高 | 每 locator 回执；无法证明时 outcome_unknown |
| owner plan 成为内部字段注入通道 | 中/高 | owner-only、typed params、未知字段拒绝、digest/domain/revision 双检 |
| 为修复缺表而创建空表造成数据假象 | 低/高 | 现有库缺业务表直接要求恢复 |
| 账本身份双进程覆盖 | 中/高 | BEGIN IMMEDIATE 与双进程测试 |
| 多域 relationship 修复变成未授权 Schema 扩张 | 中/高 | 本轮 fail-closed；另提 RFC |
| 核心 boundary 被截断改变含义 | 中/高 | 不静默截断；显式 overflow/硬错误 |
| 配置修复导致旧配置启动失败 | 中/高 | 旧配置回归测试；字段只增不删 |
| API 默认预览破坏旧脚本 | 高/中 | 明确契约、迁移说明；安全优先于危险兼容 |
| 长文分块降低写作质量 | 中/中 | 小样本盲测；默认不批量改旧库；可展开 |
| FTS 调整降低中文召回 | 中/中 | 同数据集对比后再切换；保留可回退实现 |
| 安全计划提前公开未修复缺陷细节 | 中/高 | 修复前仅本地文档，不推送公开上游 |

## 20. 文档、提交与发布约束

实施行为变更时至少同步：

- `docs/guides/memory.md` 与 `memory.zh.md`；
- 迁移相关变更同步 `docs/reference/memory-schema-v4.md` 与 `.zh.md`；
- API/CLI/UI 的真实状态、危险操作默认值与 partial 语义；
- 配置字段兼容和旧配置测试；
- 若用户明确要求发布，再更新 `CHANGELOG.md` 与 `docs/releases/<tag>.md`。

本计划新增文档包含尚未修复的缺陷分类，**在对应修复公开前不建议推送到公开上游**。本次仅本地导出，不提交、不打 tag、不触发 Release。

历史 V4 设计文档只用于追溯需求。不得因为旧文档写有 M4/M5/M6 或“已实施”就恢复旧迁移链，或将计划状态写成当前实现状态。

## 21. 最终验收清单

### 安全与治理

- [ ] 已遗忘内容不能通过 Add、Correct、API、工具、导入或计划复活。
- [ ] 模型无法自批、创建执行计划、强制修改核心状态或扩大域与受众。
- [ ] 所有新操作具有可信 actor、完整 request 和批准关联。
- [ ] Seahorse 与 JSONL 的会话/Agent 隔离通过交叉负例，模型 all_conversations 被拒绝。
- [ ] 主聊天、/btw、subagent、摘要、流式、重试和 fallback 通过用途/接收方检查，日志无正文。

### 遗忘与恢复

- [ ] 正常 Forget 与 Replay 具有相同派生数据处理。
- [ ] 长消息、重复消息、工具参数和 Seahorse parts 有真实清理测试。
- [ ] 无法核验的副本始终报告 partial/unavailable，回执不明时报告 outcome_unknown。
- [ ] `memory.enabled=false` 不停止已接受清理义务。
- [ ] 主库回滚后最新账本可重放义务，旧账本不会覆盖新账本。
- [ ] 维护备份失败时零修改；初始化与 repair 故障注入通过。

### 检索与配置

- [ ] 所有路径权限和 TTL 先于 LIMIT。
- [ ] 跨域同文、同 key、同 cluster 不互相读取、覆盖或删除；不可信非 legacy 请求 fail-closed。
- [ ] budget=0、长 boundary、长 relationship 和 1-hop 不破坏预算与 egress。
- [ ] 旧配置可加载，部分设置更新不清空安全字段。

### 管理与质量

- [ ] API/CLI/UI 区分 accepted、partial、blocked、outcome_unknown、done 和 already_gone。
- [ ] CLI 记忆子命令具备危险操作与退出码测试。
- [ ] 前端 lint/build 通过，真实 UI smoke 覆盖迁移、遗忘和任务状态。
- [ ] 质量评测含负例、禁止命中、token 与延迟；长文/时间实现未被冒充为本轮交付。
- [ ] 文档不将计划、配置字段或主库 applied 写成全部已实现/已遗忘。

## 22. 推荐开工顺序

第一批只开三条相互独立的工作线：

1. G1-01/G1-02：墓碑与模型审批，附带对抗测试。
2. G2-01/G2-03：Seahorse 隔离与后台摘要 egress。
3. G3-01/G3-02/G3-03：原子初始化、先备份和账本身份。

第二批完成 G4，再开放 G5/G6。G7 仅使用合成或授权副本测量，不阻塞安全修复。

每个实施任务在开工前必须回答：

- 改变哪个用户行为？
- 触及哪些写入口和实际发送点？
- 是否产生新的敏感副本？
- 如何遗忘、重试和恢复？
- 哪个最终状态断言证明完成？
- 哪些操作需要主人另行明确授权？

## 23. grilling 复审与最终审核记录

### 23.1 复审方式与边界

已按用户确认的 `grilling` 技能执行四轮设计树复审，并由两个独立只读代理分别核查遗忘/恢复与安全/检索交付可行性。依据用户“按推荐方案填入”的事先授权，Q1—Q20 均采用推荐答案；这不表示用户逐项重新确认，也不构成代码、数据库或发布授权。

复审期间未启动 gateway、未访问或修改真实数据库、未修改业务代码。本文档状态是**最终实施计划**，不是实现完成报告或安全认证。

### 23.2 四轮决策结果

| 问题 | 已采纳的推荐决策 |
|---|---|
| Q1 | 核心整改保持 Schema v4，不新增 v5 |
| Q2 | 主库、各后端和未知副本分别报告；done 不夸大范围 |
| Q3 | 现有库缺业务状态表时恢复备份，不自动补空表 |
| Q4 | 危险 API 默认预览，内部/未知字段拒绝 |
| Q5 | 空 recipients 保留兼容放行并明示，严格模式另行交付 |
| Q6 | budget=0 归一默认，enabled=false 才关闭长期记忆 |
| Q7 | 核心规则不静默截断，overflow 有界且可观测 |
| Q8 | 本轮不新增前端测试框架 |
| Q9 | 真实数据操作仍需单独授权，演练只用临时副本 |
| Q10 | 未修复缺陷计划在公开修复前仅本地保存 |
| Q11 | claim 时保存批准上下文；不确定结果不直接 failed |
| Q12 | pending 由维护 worker 对账，Open 不自动改业务状态 |
| Q13 | JSONL read→verify→rewrite 在单 session 临界区完成 |
| Q14 | 摘要正文/claims 清除，事务外重建 |
| Q15 | 普通干净聊天与派生数据外发分用途处理 |
| Q16 | 模型敏感动作直接拒绝，计划仅服务 owner |
| Q17 | 多域本轮 fail-closed，完整 domain 下推另提 RFC |
| Q18 | Seahorse 保持单 Engine，强制 Agent/session/conversation 隔离 |
| Q19 | 历史 forget 只读报告，不自动删除或补 journal |
| Q20 | 长文/时间实现移出本轮，保留质量与 FTS 测量 |

### 23.3 独立核查后作出的实质修订

1. 将“模型创建计划”改为“模型直接拒绝”，减少新攻击面。
2. owner 计划改为 typed params、独立完整 request ID、claim 时保存批准上下文、终态分类清理 payload。
3. DTO 闭包扩展到所有写端点，并要求未知字段返回 400。
4. actor 明确为安装实例入口身份，不伪造当前不存在的人类 principal。
5. evidence_root 改为宿主导出的版本化根协议；一个 origin event 一个根，SourceLocator/manifest 同步升级。
6. 历史 forget 严格报告-only；现代 Replay 改为 manifest entry IDs + domain，取消全库 digest 删除和 Recent(500) 上限。
7. fresh 判定与不可重建对象清单补全；v3 待迁移与 v4 损坏分开。
8. JSONL 改为活跃 store 内原子 mutate；离线 CLI 使用维护锁与 PID 双检。
9. Seahorse 摘要采用删除/脱敏，所有读写/压缩/清理共用 conversation 锁，LLM 只在事务外重建。
10. 每后端/每 locator 持久化回执；崩溃后无法证明时新增 outcome_unknown。
11. 增加 `/btw`、subagent 外发点，以及 enabled=false 仍执行清理义务的硬约束。
12. 多域、每 Agent Engine、长文分块和时间抽取不再作为本轮隐式交付。

### 23.4 最终一致性审核

| 审核项 | 结果 |
|---|---|
| 是否仍需默认 Schema v5 或新依赖 | 否 |
| 是否保留模型自批或模型计划注入路径 | 否，计划仅 owner 且 typed params |
| 是否按无 domain 历史 hash 自动删除 | 否，历史只读报告 |
| 是否混淆 JSONL 平台 message_id 与内部 ID | 否，JSONL 使用 session + 根扫描 |
| 是否混淆 Seahorse 本地 ID 与平台 ID | 否，明确建立映射 |
| 是否把两个 SQLite 库写成共同原子事务 | 否，ledger pending→主库事务→MarkApplied |
| 是否在数据库事务内调用 LLM 重建 | 否，事务内删除/脱敏，事务外重建 |
| 是否把删除后找不到直接视为成功 | 否，持久回执 + outcome_unknown |
| 是否因 memory.enabled=false 停止清理 | 否，义务独立执行 |
| 是否遗漏 `/btw`、subagent、后台摘要 egress | 否，已列入发送点矩阵 |
| 是否声称多域、长文或时间功能已实现/本轮交付 | 否，明确后续 RFC/候选 |
| 是否保留配置字段并区分 absent/false/空数组 | 是 |
| 是否将计划状态冒充实现状态 | 否 |

### 23.5 最终审核结论

**文档评审结论：通过，可作为 G0 冻结与工程拆分输入。**

通过的含义仅是：目标、范围、依赖、关键协议、失败语义、测试、回滚和发布门禁已形成可执行计划，且初稿中发现的自相矛盾已修正。它不表示：

- 任何 P0/P1 缺陷已经修复；
- 现有测试已经覆盖新协议；
- 真实数据库可以不经确认直接迁移、Replay 或 Cleanup；
- 多域 relationship、完整 domain 注入或长文摘要已经交付；
- 可以跳过主人的治理红线授权而直接实施核心算法/鉴权改动。

实施时的下一道门禁是：按 G0 先提交失败测试和精确改动清单；涉及核心治理、数据库维护、公共 API 签名或破坏性操作时，按仓库规范另行获得明确批准。

### 23.6 独立阻断终审复核

独立终审首次返回 BLOCK，指出模型动作 allowlist 不明确、owner plan 的 blocked 状态未定义及“终态”误用；同时建议限定 entry revision、统一 legacy predicate fallback 和历史 forget 操作计数。五项均已修订后，同一终审代理复核返回 **PASS**，确认未发现新的直接矛盾。

该 PASS 是最终文档一致性结论，不替代实施后的代码测试、故障注入、真实副本恢复演练或安全验收。

## 24. 0.7.20 实施结果

### 24.1 交付状态

| 工作包 | 状态 | 主要提交 |
|---|---|---|
| G0 | 完成 | `0bcfd1c7` 计划基线；对抗测试随各修复提交 |
| G1 | 完成 | `2f537c3d`、`f22f4e29`、`0d673cb9` |
| G2 | 完成 | `661b5727` |
| G3 | 完成 | `024da1bc`、`3c3d242c` |
| G4 | 完成（关闭主要缺陷） | `e8f47efa`、`661b5727`、`5aaf76c7` |
| G5 | 完成 | `77c4cc23` |
| G6 | 完成 | `78e0c368`、`8ee4bd33`、`5aaf76c7` |
| G7 | 完成合成质量门禁 | `5aaf76c7`、`3c3d242c` |

### 24.2 已关闭的关键缺陷

- Correct/API 墓碑复活；跨域 supersede；Update 改正文/状态/pinned。
- 模型自批、force 改 pinned、关系状态直写、boundary 写入和执行计划创建。
- 公开 API 内部治理字段、未知字段、伪造来源根。
- 空 actor/request、审批 claim 后无批准人、digest/domain/revision 未复核。
- Seahorse grep/expand 跨会话/Agent；模型 all_conversations；未知 ChatKind。
- `/btw`、subagent、legacy/Seahorse 摘要 egress 漏检；策略拒绝 fallback。
- `enabled=false` 停止已接受清理。
- 长证据/重复消息/工具参数 JSONL 清理；Seahorse 逐条消息与摘要清理。
- 全库哈希 Replay、跨域误删、历史 forget 无审计入口。
- 摘要 invalidated 但正文保留。
- 非原子新库初始化、v4 修复先改后备份、缺表假修复。
- 账本并发身份覆盖、request ID 非幂等、备份只比 UUID。
- TTL/Layer 后置过滤、固定块 audience 挤出、最低 200 检索预算。
- 设置部分更新清空安全字段、`memory:null` 误判、永久 data-migration 提示。
- CLI 危险操作无 revision、cleanup partial 返回 0、replay 无 dry-run。

### 24.3 验证证据（0.7.20）

- `go build -tags 'goolm stdjson' ./...`：通过。
- 核心 Go 测试：memstore、memory、seahorse、agent、tools、tools/shared、identity、session、config、API、CLI：通过。
- `CGO_ENABLED=1 go test -race`：memstore、memory、seahorse：通过。
- `pnpm run lint`：0 error（8 个既有 hook warning）；`pnpm run build`：通过；本次两个前端文件 `prettier --check` 通过。
- 全仓 `pnpm run format` 仍有 15 个无关历史文件告警，本次未批量修改。

### 24.4 与计划的剩余差异

| 项目 | 实际实现 | 保留边界 |
|---|---|---|
| pending ledger 自动对账 | 幂等 intent、pending/blocked 状态、只读审计已实现 | 无 actor/批准的旧 pending 不自动 replay；“主事务失败”自动 worker 仍需人工维护 |
| 每 locator 清理回执 | `already_absent/outcome_unknown` 字段、blocked 语义、双后端合并已实现 | 未新增持久回执表；内置后端无法证明“崩溃前已删”时保守报 unavailable/blocked |
| 多域 relationship | 跨域拒绝/fail-closed、legacy 单域 | 复合主键与完整 domain 注入仍为后续 RFC |
| 历史 forget | `memory forget-audit` 只读报告 | 无 domain 的 content hash 不自动补账本或删除 |
| G7 质量测量 | 合成正例/负例/禁止命中/TTL/layer/预算门禁 | 未提交真实私密评测集或固定设备 FTS benchmark |

这些差异已在 0.7.20 发布说明“已知边界”中公开，不把未实现能力写成已完成。
