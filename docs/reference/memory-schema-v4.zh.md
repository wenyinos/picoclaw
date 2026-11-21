# 记忆 Schema v4 迁移

PicoClaw 0.7.0 只做一件事：把长期记忆从 schema v3 迁移到 v4。旧的
`v1 → v2 → v3` 迁移链已删除，v1/v2 数据库必须先用 0.6.x 打开一次升级到
v3，才能被 0.7.0 接受。

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

## 迁移前置

1. 数据库 `schema_version = 3`：
   `sqlite3 memory.db "SELECT value FROM meta WHERE key='schema_version'"`。
2. 停止所有写入进程（gateway / CLI）。
3. 预留一份 `VACUUM INTO` 快照的磁盘空间（约等于 `memory.db` 大小）。

v1/v2 数据库会收到明确错误而不是被静默升级：先用 0.6.x 跑一次，确认
`schema_version` 为 3，再升级到 0.7.0。

## 迁移方式

启动时自动执行：

```sh
picoclaw gateway   # 任何会打开记忆库的命令
```

或显式执行并查看报告：

```sh
picoclaw memory migrate
```

迁移步骤：

1. `VACUUM INTO` 备份到 `memory/backups/memory.db.bak-<时间戳>`。
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
```

## 回滚

- 应用回滚指关闭新能力，不是降级 schema。0.6.x 二进制禁止写 v4 数据库。
- 数据回滚必须离线：把快照恢复到新路径，再重放最新 `forget-ledger.db`。
  只恢复快照会让快照之后遗忘的内容复活。
- `forget-ledger.db` 及其 HMAC 密钥必须与数据库一起备份；账本丢失时破坏性
  记忆操作按故障关闭处理，不会静默丢掉遗忘义务。

## 故障处理

| 故障 | 行为 |
|---|---|
| v1/v2 数据库 | 明确拒绝，不产生半迁移 |
| 数据库版本高于二进制 | 在任何 DDL 之前拒绝 |
| 迁移中断 | 事务回滚，下次打开重试 |
| 缺 v4 表/列 | 下次打开由同一段自愈 |
| 账本缺失 | 记忆读取可用，`forget` 故障关闭 |
| 账本已写、主库未清理 | 启动时按账本重放 |
| 主库恢复自旧备份 | `POST /api/memory/ledger/replay` 或 `picoclaw memory replay-forget` |

## 不迁移的内容

- 不从用户名、会话或“只有一个用户”猜测归属；旧数据留在
  `legacy_unassigned`，等待显式认领。
- 旧来源没有 `origin_event_id` 时保持为空，不伪造消息身份。
- v3 的硬编码数据修复（特定条目改判、生日恢复）不会重放；仍需这些修复的
  数据库请先运行 0.6.x。
