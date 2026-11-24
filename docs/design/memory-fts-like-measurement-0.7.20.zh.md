# PicoClaw 0.7.20 FTS/LIKE 同查询集测量

> 日期：2026-11-25  
> 代码基线：`v0.7.20` 后的 G7 补测提交  
> 环境：Linux x86_64，Go 1.27.4，临时 SQLite；单机冷启动后的单元测试环境  
> 结论：保留 FTS5 trigram + LIKE 混合通道；本次小样本不支持切换 tokenizer。

## 1. 复现命令

```sh
go test -tags 'goolm stdjson' ./pkg/memstore \
  -run TestFTSLikeComparisonMeasurement -count=1 -v
```

测试构建 60 条公开流程记忆、60 条私密记忆和两条短查询样本。精确/近似去重后实际保留 `documents=23`、`fts_rows=23`，因此本报告是大规模压测之外的**小样本微基准**。

同一查询集分别走：

- FTS5 `trigram`；
- LIKE fallback；
- 相同过滤条件：`status=active`、`domain=legacy_unassigned`、`audience IN ('','all')`。

## 2. 实测结果

| 查询 | 通道 | 命中 | 禁止命中 | 延迟（μs） |
|---|---|---:|---:|---:|
| 茶（1 字） | FTS trigram | 0 | 0 | 10 |
| 茶（1 字） | LIKE | 2 | 0 | 423 |
| 乌龙（2 字） | FTS trigram | 0 | 0 | 3 |
| 乌龙（2 字） | LIKE | 1 | 0 | 298 |
| 蓝色海（3 字） | FTS trigram | 6 | 0 | 679 |
| 蓝色海（3 字） | LIKE | 6 | 0 | 460 |
| 蓝色海豚（4 字） | FTS trigram | 6 | 0 | 669 |
| 蓝色海豚（4 字） | LIKE | 6 | 0 | 465 |
| `zzzqqq不存在的乱码关键词` | FTS trigram | 0 | 0 | 271 |
| `zzzqqq不存在的乱码关键词` | LIKE | 0 | 0 | 296 |

存储占用：

- `entries_fts*`：20,480 bytes；
- 文档数：23；
- FTS 行：23。

原始测量行：

```text
FTS_LIKE_MEASUREMENT {"documents":23,"fts_index_bytes":20480,"fts_rows":23,"measurements":[{"query":"茶","channel":"fts5-trigram","hits":0,"forbidden_hits":0,"latency_us":10},{"query":"茶","channel":"like","hits":2,"forbidden_hits":0,"latency_us":423},{"query":"乌龙","channel":"fts5-trigram","hits":0,"forbidden_hits":0,"latency_us":3},{"query":"乌龙","channel":"like","hits":1,"forbidden_hits":0,"latency_us":298},{"query":"蓝色海","channel":"fts5-trigram","hits":6,"forbidden_hits":0,"latency_us":679},{"query":"蓝色海","channel":"like","hits":6,"forbidden_hits":0,"latency_us":460},{"query":"蓝色海豚","channel":"fts5-trigram","hits":6,"forbidden_hits":0,"latency_us":669},{"query":"蓝色海豚","channel":"like","hits":6,"forbidden_hits":0,"latency_us":465},{"query":"zzzqqq不存在的乱码关键词","channel":"fts5-trigram","hits":0,"forbidden_hits":0,"latency_us":271},{"query":"zzzqqq不存在的乱码关键词","channel":"like","hits":0,"forbidden_hits":0,"latency_us":296}]}
```

## 3. 解释

1. **1–2 个汉字查询必须依赖 LIKE**：FTS 的短语构建要求至少 3 个 rune，短查询返回 0 是设计行为，不是索引损坏。
2. **3–4 个汉字查询两通道命中一致**：本样本均为 6 条，说明 trigram 覆盖中文子串召回，LIKE fallback 也没有引入不同结果或越权命中。
3. **延迟不可外推到生产**：23 条文档的微秒级差异受进程缓存、SQLite 页常驻和测试顺序影响。LIKE 在本样本中更快，不代表更大库存也更快。
4. **禁止命中为 0**：群聊 audience 过滤在通道 SQL 内生效，私密样本没有进入结果。
5. **去重影响规模**：60 条相似流程文本被近似合并为少量条目，因此报告中的 23 条是实际库存，不是 Add 调用次数。

## 4. 决策与后续门禁

- 保留 FTS5 trigram + LIKE 混合召回；短查询走 LIKE，长查询优先 FTS，FTS 不足 5 条时补 LIKE。
- 本次数据不足以证明替换 tokenizer、关闭 LIKE 或调整 trigram 阈值有收益。
- 如果要在真实库存上调整 tokenizer，必须先导出授权副本、按同一查询集记录 P50/P95 延迟、Recall@k、禁止命中和索引占用，再单独变更 Schema/索引。
- 本测量不包含并发写入、清理竞争、真实 Provider token 预算或移动端设备性能。

## 5. 相关代码与测试

- `pkg/memstore/fts.go`：FTS/LIKE SQL 通道与过滤前置。
- `pkg/memstore/g7_fts_compare_test.go`：同查询集微基准和机器可读结果。
- `pkg/memstore/g7_quality_test.go`：正例、负例、私密隔离、TTL、1-hop 和预算门禁。
