package memstore

import (
	"context"
	"strings"
	"testing"
)

func TestLikeFallbackTreatsWildcardsLiterally(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	exact, err := s.Add(ctx, AddInput{Text: "进度 100% 完成", Layer: LayerProcedural})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{Text: "进度 1000 完成", Layer: LayerProcedural}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.searchLike(ctx, "100%", 20, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != exact.Entry.ID {
		t.Fatalf("literal wildcard query ids = %+v", ids)
	}
	ids, err = s.searchLike(ctx, "100_", 20, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("underscore wildcard query returned %+v", ids)
	}
}

func TestCoreBoundaryOverflowIsObservable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	longRule := strings.Repeat("不可丢弃的核心安全边界", 120)
	if _, err := s.Add(ctx, AddInput{
		Text: longRule, Layer: LayerBoundary, Audience: "all", PinnedCore: true,
	}); err != nil {
		t.Fatal(err)
	}
	fixed, usage := s.BuildFixedContextFor(ctx, 120, false, "pico", ChatKindPrivate, nil)
	if !strings.Contains(fixed, "不可丢弃的核心安全边界") {
		t.Fatal("core boundary was silently dropped")
	}
	if !usage.CoreOverflow || usage.OverflowTokens <= 0 {
		t.Fatalf("usage = %+v, want visible core overflow", usage)
	}
	if usage.TotalTokens != usage.BaseTokens+usage.BoundaryTokens {
		t.Fatalf("usage totals do not add up: %+v", usage)
	}
}
