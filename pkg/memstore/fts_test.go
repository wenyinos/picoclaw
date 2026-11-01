package memstore

import (
	"context"
	"testing"
)

func TestFTSShortQueryFallsBackToLike(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{
		Text: "用户的生日是三月五日", Layer: LayerProfile, Subject: "user",
		Predicate: "birthday", Facet: "日期", Value: "三月五日", Importance: 0.9,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "用户同意在周末见面", Layer: LayerProfile, Importance: 0.8,
	}); err != nil {
		t.Fatal(err)
	}

	// Two-character Chinese queries must not return zero just because FTS5
	// trigram needs three characters.
	for _, q := range []string{"生日", "同意"} {
		entries, err := s.SearchEntries(ctx, SearchOptions{Query: q, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatalf("short query %q returned no hits", q)
		}
	}

	// A four-character phrase goes through FTS and still matches.
	entries, err := s.SearchEntries(ctx, SearchOptions{Query: "生日是三月", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Predicate != "birthday" {
		t.Fatalf("phrase query missed the birthday entry: %+v", entries)
	}
}

func TestFTSUsageUpdateDoesNotRebuild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{Text: "FTS 触发器只监听内容列", Layer: LayerProcedural})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.FTSCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before != 1 {
		t.Fatalf("fts rows = %d, want 1", before)
	}
	// use_count updates must not delete/reinsert FTS rows.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE entries SET use_count=use_count+1, last_used_at=1 WHERE id=?`, added.Entry.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.FTSCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("use_count update changed FTS rows: %d -> %d", before, after)
	}

	// Content updates are reflected by the trigger.
	if _, err := s.Update(ctx, added.Entry.ID, AddInput{Text: "FTS 触发器同步新内容"}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.searchFTS(ctx, "同步新内容", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("FTS did not follow the content update: %+v", hits)
	}

	// Rebuild is consistent.
	n, err := s.RebuildFTS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var total int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&total)
	if n != total {
		t.Fatalf("rebuild indexed %d, entries %d", n, total)
	}
}

func TestBuildFTSQuery(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantLike bool
	}{
		{"", false},
		{"生", true},
		{"生日", true},
		{"边界授权", false},
		{`"quoted"`, false},
	} {
		match, like := buildFTSQuery(tc.in)
		if tc.wantLike && len(like) == 0 {
			t.Fatalf("%q should use LIKE", tc.in)
		}
		if !tc.wantLike && match == "" && len(like) > 0 {
			t.Fatalf("%q should use MATCH", tc.in)
		}
	}
}
