# 记忆 Schema v4 迁移

PicoClaw 当前主库 schema 仍为 v4。0.7.0 提供 `v3 → v4` 迁移；0.7.25 不改变
主库版本，只对独立的 `forget-ledger.db` 增加 `forget_receipts` 与
`forget_journal.blocked_reason/updated_at`。旧的 `v1 → v2 → v3` 迁移链已删除，
v1/v2 数据库必须先用 0.6.x 打开一次升级到 v3，才能被当前版本接受。

## v4 新增内容

| 领域 | 对象 |
|---|---|
| 归属 | `memory_domains`、`memory_actor_bindings`、`memory_processing_grants` |
| 条目治理 | `entries.domain_id`、`row_revision`、`archived_from`、`event_start`、`event_end`、`sensitivity`、`qualifiers`、`qualifiers_hash`、`evidence_status`、`freshness` |
| 证据 | `sources.origin_event_id`、`origin_revision`、`confirmation_event_id`、`evidence_root`；`memory_origin_events` |
| 派生视图 | `memory_summary_evidence`、`memory_dependencies`、簇/摘要的 `generation` 与域列 |
| 持久任务 | `memory_jobs`、`memory_change_plans` |
| 伴侣能力 | `memory_commitments`、`memory_recalls`、`memory_recall_feedback` |
| 遗忘安全 | 独立 `memory/forget-ledger.db`（HMAC 选择器 + 可恢复目标清单） |

## 0.7.25 账本增量（不改变主库 v4）

| 对象 | 说明 |
|---|---|
| `forget_receipts` | 每个 intent 按 `backend + locator_key` 保存清理回执与状态（`removed/verified_absent/unavailable/outcome_unknown`），主键 `(intent_seq, backend, locator_key)` |
| `forget_journal.blocked_reason` | blocked 原因码，只存在独立账本 |
| `forget_journal.updated_at` | 最近状态更新时间 |

- 主库 `memory.db` 的 `schema_version` 仍为 4；不需要、也不得因此增加 v5。
- 账本增量在打开账本时以幂等 DDL 补齐（`CREATE TABLE IF NOT EXISTS` 与带存在性守卫的 `ALTER TABLE`），不写主库版本。
- `memory_origin_events` 已在生产路径接线：入站自动抽取和 `memory` 工具写路径由宿主生成稳定 `origin_event_id`，`sources.origin_event_id` 指向它；同一事实来自多条消息时保留多个 `sources`。该表不是只建表未使用。
- pending/blocked intent 或 pending/running/blocked/cancelled cleanup 存在时，恢复门禁让长期记忆 fail closed；普通聊天可无记忆运行。详见 [长期记忆指南](../guides/memory.zh.md)。

## 0.7.26 一次性升级向导

主库 Schema 仍为 v4，不新增表或版本。`/memory` →「数据库修复」新增向导：

- 先只读预览，再要求 owner 确认；
- 强制生成主库与账本备份；
- 需要时执行 v3 → v4，并只补已知 `legacy_unassigned` 默认域；
- 将历史 `cancelled/blocked/failed` cleanup 任务结案为 `legacy_closed`；
- 记录 `legacy_cleanup_unverified` 操作审计，并重新计算恢复状态。

`legacy_closed` 不是“删除已验证”，只是历史义务的显式结案，不会让未来的 blocked/pending cleanup 透传；报告保留 `residual_risk` 与备份路径。向导返回 `restart_required=true`；完成后需重启 gateway/launcher，再以 `recovery-status` 确认运行进程已恢复。

## 迁移前置

1. 数据库 `schema_version = 3`：
   `sqlite3 memory.db "SELECT value FROM meta WHERE key='schema_version'"`。
2. 停止所有写入进程（gateway / CLI）。
3. 预留一份 `VACUUM INTO` 快照的磁盘空间（约等于 `memory.db` 大小）。

v1/v2 数据库会收到明确错误而不是被静默升级：先用 0.6.x 跑一次，确认
`schema_version` 为 3，再升级到 0.7.0。

## 迁移方式

**0.7.2 起不再在启动时自动迁移。** 已有 v3 数据库（或缺对象的 v4 库）会以故障关闭
模式打开：仪表盘和 CLI 仍能打开它以便执行迁移，但普通记忆读写会返回
`schema migration required`。全新数据库直接初始化为 v4。

显式执行一次：

```sh
picoclaw memory migrate
```

或使用 `/memory` → “数据库迁移”。页面会显示当前 schema、待迁移状态、备份路径和
迁移后的统计，确认后才执行。

迁移步骤：

1. `VACUUM INTO` 备份到 `memory/backups/`：主库 `memory.db.bak-<时间戳>` 与账本 `forget-ledger.db.bak-<时间戳>` 各一份。
2. 创建 v4 新表（`CREATE TABLE IF NOT EXISTS`）。
3. 用带守卫的 `ALTER TABLE` 补齐 v4 列。
4. 逐字段重建 `sources`：证据原文、模型、确认标志、`message_id`、置信度全部保留；
   旧表缺失的列才用显式默认值，已有字段不会丢。
5. 建立隔离域 `legacy_unassigned`。
6. 精确指纹索引改为 `(domain_id, content_hash)`。
7. FTS 缺失时重建投影。
8. 整段成功后才写入 `schema_version = 4`。

重复执行安全：所有语句幂等，第二次运行报告零变更。

## 迁移后验证

```sh
sqlite3 memory.db "SELECT value FROM meta WHERE key='schema_version'"   # 4
sqlite3 memory.db "PRAGMA integrity_check"                              # ok
sqlite3 memory.db "SELECT COUNT(*) FROM entries"                        # 与迁移前一致
sqlite3 memory.db "SELECT COUNT(*) FROM sources"                        # 与迁移前一致
picoclaw memory inspect <entry-id>                                      # sources 仍有 evidence_text
picoclaw memory ledger-verify --backup memory/backups/forget-ledger.db.bak-<时间戳>  # match: true
```

`ledger-verify` 比对备份与当前账本的 UUID/key_id，并输出 backup/active max seq、applied seq 与 pending 数量；身份不一致时返回非零退出码，不能用于重放遗忘。

## 回滚

- 应用回滚指关闭新能力，不是降级 schema。0.6.x 二进制禁止写 v4 数据库。
- 数据回滚必须离线：把主库快照恢复到新路径，再重放最新 `forget-ledger.db`。
  只恢复快照会让快照之后遗忘的内容复活。
- 0.7.10 起迁移/修复生成的备份同时包含主库与账本快照；两者必须成对恢复，并用
  `picoclaw memory ledger-verify --backup <账本快照>` 校验身份。
- 0.7.20 起所有实际迁移/v4 修复都在写入前创建新备份；全新库初始化在单事务内完成。
- 0.7.25 起 `replay-forget` 默认只输出 preview，只重放 eligible intent，`blocked/unsupported/unapproved` 均不执行；manifest v2 逐目标记录 domain 与 revision，legacy manifest 用 HMAC digest proof；API 需 `confirm=true`，CLI 需 `--yes`。
- 历史 forget 用 `picoclaw memory forget-audit` 只读审计，不自动补账本或删除。
- `forget-ledger.db` 及其 HMAC 密钥必须与数据库一起备份；账本缺失或身份损坏时
  破坏性记忆操作按故障关闭处理，不会静默新建空账本或丢掉遗忘义务。

## 故障处理

| 故障 | 行为 |
|---|---|
| v1/v2 数据库 | 明确拒绝，不产生半迁移 |
| 数据库版本高于二进制 | 在任何 DDL 之前拒绝 |
| 迁移中断 | 事务回滚，下次打开重试 |
| 缺索引/触发器/FTS | 显式备份后由同一段幂等逻辑修复 |
| 缺业务/义务/审计表 | 返回 `non_repairable`，必须恢复备份，不创建空表 |
| 账本缺失 | 记忆读取可用，`forget` 故障关闭 |
| 账本身份损坏 | 记忆读取可用，`forget`/`replay-forget` 故障关闭，不重置身份 |
| 账本已写、主库未清理 | 启动时按账本对账；仅 eligible intent 会被重放 |
| 存在 pending/blocked intent 或未决 cleanup | 长期记忆 fail closed（`ErrMemoryNotReady`），普通聊天可无记忆运行；用 `picoclaw memory recovery-status` 检查 |
| cleanup 回执缺失或 `outcome_unknown` | `memory cleanup` 返回非零，不能判定完成；用 `requeue-job` 创建 successor |
| 主库恢复自旧备份 | `POST /api/memory/ledger/replay` 或 `picoclaw memory replay-forget --yes`；先 dry-run 检查 |

## 不迁移的内容

- 不从用户名、会话或“只有一个用户”猜测归属；旧数据留在
  `legacy_unassigned`，等待显式认领。
- 旧来源没有 `origin_event_id` 时保持为空，不伪造消息身份。
- v3 的硬编码数据修复（特定条目改判、生日恢复）不会重放；仍需这些修复的
  数据库请先运行 0.6.x。
