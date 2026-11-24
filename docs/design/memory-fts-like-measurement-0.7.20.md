# PicoClaw 0.7.20 FTS/LIKE Same-Query-Set Measurement

> Date: 2026-11-25  
> Code baseline: G7 follow-up commit after `v0.7.20`  
> Environment: Linux x86_64, Go 1.27.4, temporary SQLite, unit-test microbenchmark  
> Verdict: keep the hybrid FTS5 trigram + LIKE channels; this small sample does not justify a tokenizer change.

## 1. Reproduction

```sh
go test -tags 'goolm stdjson' ./pkg/memstore \
  -run TestFTSLikeComparisonMeasurement -count=1 -v
```

The test creates 60 public procedural memories, 60 private memories and two short-query rows. Exact/near deduplication leaves `documents=23` and `fts_rows=23`, so this is a **small-sample microbenchmark**, not a production-scale load test.

Both channels use the same filter:

`status=active`, `domain=legacy_unassigned`, `audience IN ('','all')`.

## 2. Measurements

| Query | Channel | Hits | Forbidden hits | Latency (μs) |
|---|---|---:|---:|---:|
| 茶 (1 rune) | FTS trigram | 0 | 0 | 10 |
| 茶 (1 rune) | LIKE | 2 | 0 | 423 |
| 乌龙 (2 runes) | FTS trigram | 0 | 0 | 3 |
| 乌龙 (2 runes) | LIKE | 1 | 0 | 298 |
| 蓝色海 (3 runes) | FTS trigram | 6 | 0 | 679 |
| 蓝色海 (3 runes) | LIKE | 6 | 0 | 460 |
| 蓝色海豚 (4 runes) | FTS trigram | 6 | 0 | 669 |
| 蓝色海豚 (4 runes) | LIKE | 6 | 0 | 465 |
| `zzzqqq不存在的乱码关键词` | FTS trigram | 0 | 0 | 271 |
| `zzzqqq不存在的乱码关键词` | LIKE | 0 | 0 | 296 |

Storage:

- `entries_fts*`: 20,480 bytes;
- documents: 23;
- FTS rows: 23.

Raw measurement:

```text
FTS_LIKE_MEASUREMENT {"documents":23,"fts_index_bytes":20480,"fts_rows":23,"measurements":[{"query":"茶","channel":"fts5-trigram","hits":0,"forbidden_hits":0,"latency_us":10},{"query":"茶","channel":"like","hits":2,"forbidden_hits":0,"latency_us":423},{"query":"乌龙","channel":"fts5-trigram","hits":0,"forbidden_hits":0,"latency_us":3},{"query":"乌龙","channel":"like","hits":1,"forbidden_hits":0,"latency_us":298},{"query":"蓝色海","channel":"fts5-trigram","hits":6,"forbidden_hits":0,"latency_us":679},{"query":"蓝色海","channel":"like","hits":6,"forbidden_hits":0,"latency_us":460},{"query":"蓝色海豚","channel":"fts5-trigram","hits":6,"forbidden_hits":0,"latency_us":669},{"query":"蓝色海豚","channel":"like","hits":6,"forbidden_hits":0,"latency_us":465},{"query":"zzzqqq不存在的乱码关键词","channel":"fts5-trigram","hits":0,"forbidden_hits":0,"latency_us":271},{"query":"zzzqqq不存在的乱码关键词","channel":"like","hits":0,"forbidden_hits":0,"latency_us":296}]}
```

## 3. Interpretation

1. One- and two-rune queries must use LIKE because FTS phrase construction requires at least three runes. A zero FTS result is expected.
2. Three- and four-rune queries returned identical hits in both channels (6 each). Trigram covers Chinese substring recall, and the LIKE fallback introduced neither extra results nor forbidden rows.
3. Latency must not be extrapolated to production. With 23 documents, microsecond differences are dominated by page cache and test ordering.
4. Forbidden hits were zero. Group audience filtering is part of each channel's SQL.
5. Deduplication matters: 60 similar procedural texts collapsed to a small number of rows. The 23 documents are actual inventory, not Add call count.

## 4. Decision and Follow-up Gate

- Keep FTS5 trigram + LIKE. Short queries use LIKE; longer queries prefer FTS and add LIKE when FTS returns fewer than five hits.
- This run does not justify replacing the tokenizer, disabling LIKE or changing trigram thresholds.
- Any tokenizer change on real inventory requires an authorized copy, the same query set, P50/P95 latency, Recall@k, forbidden hits and index-size measurements, then a separately reviewed schema/index change.
- This measurement excludes concurrent writes, cleanup races, real provider token budgets and mobile-device performance.

## 5. References

- `pkg/memstore/fts.go` — FTS/LIKE channels with pre-limit filtering.
- `pkg/memstore/g7_fts_compare_test.go` — reproducible same-query-set microbenchmark.
- `pkg/memstore/g7_quality_test.go` — positive/negative, private isolation, TTL, 1-hop and budget gates.
