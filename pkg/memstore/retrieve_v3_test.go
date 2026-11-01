package memstore

import (
	"context"
	"testing"
)

func TestAnchorQueriesRankPinnedFacetFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// A high-importance noise entry that mentions every anchor in passing.
	if _, err := s.Add(ctx, AddInput{
		Text:  "这是一份设计说明，提到了本质定调、角色卡、边界授权、尺度与氛围、创作方向等词。",
		Layer: LayerProcedural, Importance: 0.95, Predicate: "工程",
	}); err != nil {
		t.Fatal(err)
	}
	anchors := []string{"本质定调", "角色卡", "边界授权", "尺度与氛围", "创作方向"}
	for _, facet := range anchors {
		if _, err := s.Add(ctx, AddInput{
			Text: "T0 铁律正文：" + facet, Layer: LayerProcedural, Realm: RealmFictional,
			Audience: "private", Predicate: "写作规则", Facet: facet,
			Importance: 0.8, PinnedCore: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range anchors {
		entries, err := s.SearchEntries(ctx, SearchOptions{Query: q, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatalf("%q returned nothing", q)
		}
		if entries[0].Facet != q {
			t.Fatalf("%q top-1 = %q (facet %q), want the pinned anchor", q, entries[0].Text, entries[0].Facet)
		}
	}
}

func TestFixedUsageAccountsForBoundary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if _, err := s.Add(ctx, AddInput{
			Text:  "很长的用户画像段落 " + string(rune('a'+i)) + " 细节细节细节细节细节细节细节细节细节细节",
			Layer: LayerProfile, Audience: "private", Importance: 0.9,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "边界一：不要在公开渠道使用亲密称呼", Layer: LayerBoundary,
		Audience: "private", Importance: 0.95, PinnedCore: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "边界二：不要替用户做财务决定", Layer: LayerBoundary,
		Audience: "private", Importance: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	fixed, usage := s.BuildFixedContext(ctx, 540, true, "pico", []string{"pico"})
	if fixed == "" {
		t.Fatal("empty fixed context")
	}
	if usage.BaseTokens > 540 {
		t.Fatalf("base tokens %d exceed the fixed budget", usage.BaseTokens)
	}
	if usage.BoundaryTokens == 0 {
		t.Fatal("boundary tokens were not accounted")
	}
	if usage.TotalTokens != usage.BaseTokens+usage.BoundaryTokens {
		t.Fatalf("total %d != base %d + boundary %d",
			usage.TotalTokens, usage.BaseTokens, usage.BoundaryTokens)
	}
	if usage.TotalTokens > 540+usage.BoundaryBudget {
		t.Fatalf("fixed block overflowed by more than the boundary cap: %+v", usage)
	}
}

func TestCapsuleChannelIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{
		Text: "T0 铁律：不准停", Layer: LayerProcedural, Realm: RealmFictional,
		Audience: "private", Predicate: "写作规则", Facet: "边界授权", PinnedCore: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := nowMillis()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_clusters(id,realm,title,topic,status,created_at,updated_at)
		 VALUES('capsule:test','fictional','测试胶囊','t','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_cluster_members(cluster_id,entry_id,ordinal) VALUES('capsule:test',?,1)`,
		entry.Entry.ID); err != nil {
		t.Fatal(err)
	}

	public, err := s.LoadCapsule(ctx, "capsule:test", "discord", []string{"pico"}, false, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if len(public.Items) != 0 {
		t.Fatalf("fictional capsule leaked to a public channel: %+v", public)
	}
	off, err := s.LoadCapsule(ctx, "capsule:test", "pico", []string{"pico"}, false, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Items) != 0 {
		t.Fatalf("fictional capsule leaked with inject_fictional=false: %+v", off)
	}
	private, err := s.LoadCapsule(ctx, "capsule:test", "pico", []string{"pico"}, true, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if len(private.Items) != 1 || private.Items[0].EntryID != entry.Entry.ID {
		t.Fatalf("capsule missing for private channel: %+v", private)
	}
	if private.TotalTokens == 0 {
		t.Fatal("capsule token count is zero")
	}
}
