package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

type channelMeasurement struct {
	Query         string `json:"query"`
	Channel       string `json:"channel"`
	Hits          int    `json:"hits"`
	ForbiddenHits int    `json:"forbidden_hits"`
	LatencyMicros int64  `json:"latency_us"`
}

// TestFTSLikeComparisonMeasurement runs FTS trigram and LIKE over one query
// set and emits a machine-readable measurement line. It is a reproducible
// G7-04 gate, not a claim about every deployment's hardware.
func TestFTSLikeComparisonMeasurement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		if _, err := s.Add(ctx, AddInput{
			Text:  fmt.Sprintf("测量文档 %03d 蓝色海豚 部署流程", i),
			Layer: LayerProcedural, Audience: "all",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Add(ctx, AddInput{
			Text:  fmt.Sprintf("私密标记 %03d", i),
			Layer: LayerProfile, Audience: "private",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "用户喜欢乌龙茶", Layer: LayerProfile, Audience: "all", Subject: "user", Predicate: "drink", Facet: "tea",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "下午茶偏好甜点", Layer: LayerProfile, Audience: "all", Subject: "user", Predicate: "snack", Facet: "tea",
	}); err != nil {
		t.Fatal(err)
	}
	filter := `e.status='active' AND e.domain_id='legacy_unassigned' AND e.audience IN ('','all')`
	var filterArgs []any
	queries := []string{"茶", "乌龙", "蓝色海", "蓝色海豚", "zzzqqq不存在的乱码关键词"}
	var measurements []channelMeasurement
	for _, query := range queries {
		start := time.Now()
		ftsHits, ftsErr := s.searchFTS(ctx, query, 20, filter, filterArgs)
		if ftsErr != nil {
			t.Fatal(ftsErr)
		}
		measurements = append(measurements, channelMeasurement{
			Query: query, Channel: "fts5-trigram", Hits: len(ftsHits),
			ForbiddenHits: forbiddenHits(t, s, ctx, ftsIDs(ftsHits)), LatencyMicros: time.Since(start).Microseconds(),
		})
		start = time.Now()
		likeHits, likeErr := s.searchLike(ctx, query, 20, filter, filterArgs)
		if likeErr != nil {
			t.Fatal(likeErr)
		}
		measurements = append(measurements, channelMeasurement{
			Query: query, Channel: "like", Hits: len(likeHits),
			ForbiddenHits: forbiddenHits(t, s, ctx, likeHits), LatencyMicros: time.Since(start).Microseconds(),
		})
	}
	var docCount, ftsRows, indexBytes int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&docCount); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name LIKE 'entries_fts%'`).Scan(&indexBytes); err != nil {
		t.Fatal(err)
	}
	for _, m := range measurements {
		if m.ForbiddenHits != 0 {
			t.Fatalf("forbidden hit in %+v", m)
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"documents": docCount, "fts_rows": ftsRows, "fts_index_bytes": indexBytes,
		"measurements": measurements,
	})
	t.Logf("FTS_LIKE_MEASUREMENT %s", raw)
}

func ftsIDs(hits []ftsHit) []string {
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.EntryID)
	}
	return ids
}

func forbiddenHits(t *testing.T, s *Store, ctx context.Context, ids []string) int {
	t.Helper()
	if len(ids) == 0 {
		return 0
	}
	count := 0
	for _, id := range ids {
		var audience string
		if err := s.db.QueryRowContext(ctx, `SELECT audience FROM entries WHERE id=?`, id).Scan(&audience); err != nil {
			t.Fatal(err)
		}
		if audience != "" && audience != "all" {
			count++
		}
	}
	return count
}
