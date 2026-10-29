package memstore

import (
	"context"
	"strings"
	"testing"
)

func TestBuildFixedContext_BoundaryAlwaysIncluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{
		Text:  "用户画像长文本 " + strings.Repeat("细节", 200),
		Layer: LayerProfile, Audience: "private", Importance: 0.9,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text:  "BOUNDARY_SENTINEL 不要在公开渠道使用亲密称呼",
		Layer: LayerBoundary, Audience: "private", Importance: 0.95,
	}); err != nil {
		t.Fatal(err)
	}

	got := s.BuildFixedContext(ctx, 20, false, "pico", []string{"pico"})
	if !strings.Contains(got, "BOUNDARY_SENTINEL") {
		t.Fatalf("boundary was evicted by the fixed-block budget: %s", got)
	}
}

func TestBuildFixedContext_BoundaryAudienceAndFictionalFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, in := range []AddInput{
		{Text: "BOUNDARY_PUBLIC", Layer: LayerBoundary, Audience: "all"},
		{Text: "BOUNDARY_PRIVATE", Layer: LayerBoundary, Audience: "private"},
		{Text: "BOUNDARY_FICTIONAL", Layer: LayerBoundary, Realm: RealmFictional, Audience: "all"},
	} {
		if _, err := s.Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	public := s.BuildFixedContext(ctx, 600, false, "", nil)
	if !strings.Contains(public, "BOUNDARY_PUBLIC") {
		t.Fatalf("public boundary missing: %s", public)
	}
	if strings.Contains(public, "BOUNDARY_PRIVATE") || strings.Contains(public, "BOUNDARY_FICTIONAL") {
		t.Fatalf("private/fictional boundary leaked into public context: %s", public)
	}

	private := s.BuildFixedContext(ctx, 600, false, "pico", []string{"pico"})
	if !strings.Contains(private, "BOUNDARY_PRIVATE") {
		t.Fatalf("private boundary missing in private channel: %s", private)
	}
	if strings.Contains(private, "BOUNDARY_FICTIONAL") {
		t.Fatalf("fictional boundary injected without inject_fictional: %s", private)
	}

	fictional := s.BuildFixedContext(ctx, 600, true, "pico", []string{"pico"})
	if !strings.Contains(fictional, "BOUNDARY_FICTIONAL") {
		t.Fatalf("fictional boundary missing when enabled: %s", fictional)
	}
}
