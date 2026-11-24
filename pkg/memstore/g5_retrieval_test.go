package memstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFixedContextFiltersAudienceBeforeLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 45; i++ {
		if _, err := s.Add(ctx, AddInput{
			Text: fmt.Sprintf("private profile %d", i), Layer: LayerProfile,
			Audience: "private", Importance: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "PUBLIC GROUP SENTINEL", Layer: LayerProfile,
		Audience: "all", Importance: 0.1,
	}); err != nil {
		t.Fatal(err)
	}
	fixed, _ := s.BuildFixedContextFor(ctx, 2000, false, "pico", ChatKindGroup, nil)
	if !strings.Contains(fixed, "PUBLIC GROUP SENTINEL") {
		t.Fatalf("public profile was crowded out before filtering: %q", fixed)
	}
	if strings.Contains(fixed, "private profile") {
		t.Fatalf("group fixed context leaked private profiles: %q", fixed)
	}
}

func TestExpiredEntriesAreInvisibleEverywhere(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	expired := time.Now().Add(-time.Hour).UnixMilli()
	entry, err := s.Add(ctx, AddInput{
		Text: "expired procedural needle", Layer: LayerProcedural, ExpiresAt: &expired,
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := s.Search(ctx, SearchOptions{
		Query: "expired procedural needle", AllLayers: true, ExcludeExpired: true,
		DomainID: DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range results {
		if got.ID == entry.Entry.ID {
			t.Fatal("expired entry was returned by retrieval")
		}
	}
	related, err := s.ExpandOneHop(ctx, []Entry{*entry.Entry}, 3, SearchOptions{
		AllLayers: true, ExcludeExpired: true, DomainID: DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(related) != 0 {
		t.Fatalf("expired one-hop expansion returned %+v", related)
	}
	if text := s.BuildRetrievalContextFor(
		ctx, "expired procedural needle", "pico", ChatKindPrivate, 1200, false, nil,
	); strings.Contains(text, "expired procedural needle") {
		t.Fatalf("retrieval prompt included expired memory: %q", text)
	}
}

func TestOneHopHonorsMainRetrievalLayers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seed, err := s.Add(ctx, AddInput{Text: "lake scene seed", Layer: LayerEpisodic})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Add(ctx, AddInput{Text: "PROFILE SHOULD NOT EXPAND", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelation(ctx, Relation{
		SourceID: seed.Entry.ID, TargetID: profile.Entry.ID, RelationType: RelRelatedTo,
	}); err != nil {
		t.Fatal(err)
	}
	related, err := s.ExpandOneHop(ctx, []Entry{*seed.Entry}, 3, SearchOptions{
		DomainID: DomainLegacyUnassigned, ExcludeExpired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range related {
		if e.ID == profile.Entry.ID {
			t.Fatal("one-hop bypassed the main retrieval layer policy")
		}
	}
}

func TestZeroRetrievalBudgetInjectsNoRetrievalBlock(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{Text: "budget needle", Layer: LayerProcedural}); err != nil {
		t.Fatal(err)
	}
	if got := s.BuildRetrievalContextFor(
		ctx, "budget needle", "pico", ChatKindPrivate, 0, false, nil,
	); got != "" {
		t.Fatalf("zero budget returned retrieval block: %q", got)
	}
}
