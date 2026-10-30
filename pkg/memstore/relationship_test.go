package memstore

import (
	"context"
	"strings"
	"testing"
)

func TestPersonaVersioningAndChannelFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p1, err := s.UpsertPersonaScoped(ctx, "companion", "姐姐", "私密人设 v1", true, RealmFictional, "private", "user")
	if err != nil {
		t.Fatal(err)
	}
	if p1.Version != 1 || p1.Realm != RealmFictional || p1.Audience != "private" {
		t.Fatalf("unexpected persona: %+v", p1)
	}
	if _, err := s.UpsertPersonaScoped(ctx, "companion", "姐姐", "私密人设 v2", true, RealmFictional, "private", "user"); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListPersonas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("personas = %d, want 2", len(list))
	}

	got, err := s.GetActivePersonaForChannel(ctx, "pico", []string{"pico"}, true)
	if err != nil || got == nil || got.Content != "私密人设 v2" {
		t.Fatalf("private persona = %+v, %v", got, err)
	}
	got, err = s.GetActivePersonaForChannel(ctx, "", nil, true)
	if err != nil || got != nil {
		t.Fatalf("public persona should be hidden: %+v, %v", got, err)
	}
	got, err = s.GetActivePersonaForChannel(ctx, "pico", []string{"pico"}, false)
	if err != nil || got != nil {
		t.Fatalf("fictional persona should be hidden without inject_fictional: %+v, %v", got, err)
	}
}

func TestFacetConflictRules(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := AddInput{
		Text: "用户希望被称为阿泽", Realm: RealmReal, Layer: LayerProfile,
		Subject: "user", Predicate: "nickname", Facet: "称呼", Value: "阿泽",
	}
	if _, err := s.Add(ctx, base); err != nil {
		t.Fatal(err)
	}
	// Different facet is a different side of the same predicate and must coexist.
	other := base
	other.Text = "用户希望 AI 被称为小P"
	other.Subject = "ai"
	other.Value = "小P"
	other.Facet = "称呼"
	if _, err := s.Add(ctx, other); err != nil {
		t.Fatal(err)
	}
	// Same scalar fact in the same facet supersedes.
	newer := base
	newer.Text = "请把我的称呼改成阿明"
	newer.Value = "阿明"
	res, err := s.Add(ctx, newer)
	if err != nil || res.Action != "superseded" {
		t.Fatalf("scalar supersede = %+v, %v", res, err)
	}

	// Narrative entries with the same predicate but different facets coexist.
	n1 := AddInput{
		Text: "身份关系侧面一", Realm: RealmFictional, Layer: LayerRelationship,
		Subject: "姐弟故事", Predicate: "身份关系", Facet: "家庭压力", Value: "一",
	}
	n2 := AddInput{
		Text: "身份关系侧面二", Realm: RealmFictional, Layer: LayerRelationship,
		Subject: "姐弟故事", Predicate: "身份关系", Facet: "公开身份", Value: "二",
	}
	if _, err := s.Add(ctx, n1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, n2); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchEntries(ctx, SearchOptions{Realm: RealmFictional, Layer: LayerRelationship, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("relationship facets = %d, want 2: %+v", len(got), got)
	}

	// Explicit supersede still works for narrative entries.
	res, err = s.Add(ctx, AddInput{
		Text: "身份关系侧面二（新）", Realm: RealmFictional, Layer: LayerRelationship,
		Subject: "姐弟故事", Predicate: "身份关系", Facet: "公开身份", Value: "二新",
		SupersedesID: got[0].ID,
	})
	if err != nil || res.Action != "superseded" {
		t.Fatalf("explicit supersede = %+v, %v", res, err)
	}
}

func TestRelationshipRetrievalQuota(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i, text := range []string{
		"关系机制 海 一", "关系机制 海 二", "关系机制 海 三",
	} {
		if _, err := s.Add(ctx, AddInput{
			Text: text, Realm: RealmReal, Layer: LayerRelationship,
			Audience: "all", Importance: 0.9, Facet: "测试" + string(rune('1'+i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, text := range []string{
		"事件 海 一", "事件 海 二", "事件 海 三", "事件 海 四",
	} {
		if _, err := s.Add(ctx, AddInput{
			Text: text, Realm: RealmReal, Layer: LayerEpisodic,
			Audience: "all", Importance: 0.8, Facet: "测试" + string(rune('1'+i)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	got := s.BuildRetrievalContext(ctx, "海", "", 2000, false, nil)
	if strings.Count(got, "[relationship/real]") != 2 {
		t.Fatalf("relationship quota not applied: %s", got)
	}
	if strings.Count(got, "[episodic/real]") > 6 {
		t.Fatalf("episodic quota not applied: %s", got)
	}
}

func TestUsageBuffer(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	res, err := s.Add(ctx, AddInput{Text: "usage test", Layer: LayerProcedural})
	if err != nil {
		t.Fatal(err)
	}
	s.RecordUsage([]string{res.Entry.ID, res.Entry.ID})
	s.flushUsage(ctx)
	got, err := s.Get(ctx, res.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UseCount != 2 || got.LastUsedAt == nil {
		t.Fatalf("usage not recorded: %+v", got)
	}
}
