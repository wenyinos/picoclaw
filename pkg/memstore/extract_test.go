package memstore

import (
	"context"
	"testing"
	"time"
)

func TestExtractRoleplayDialogueDoesNotBecomeProfile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// This is the exact shape of the reference database's pollution entry:
	// a line of role-play that contains "记住" mid-sentence.
	dialogue := "玩之前我会告诉他，今晚，我想体验被你强行占有的感觉。你可以撕扯，可以压制，可以野蛮。" +
		"记住，我所有的反抗、哭喊、甚至打骂，都是为了刺激你。你绝对不准停。"
	results, err := s.ExtractTurnAt(ctx, dialogue, "", "s1", time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("dialogue produced %d memories: %+v", len(results), results)
	}
	all, _ := s.List(ctx, SearchOptions{Limit: 10})
	if len(all) != 0 {
		t.Fatalf("store is not empty: %+v", all)
	}
}

func TestExtractImperativeUsesPredicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	results, err := s.ExtractTurnAt(ctx, "记住我对花生过敏", "", "s1", time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("imperative produced %d results", len(results))
	}
	entry := results[0].Entry
	if entry == nil {
		t.Fatal("no entry")
	}
	if entry.Predicate != "用户画像" || entry.Facet != "明确要求记住" {
		t.Fatalf("predicate/facet = %q/%q", entry.Predicate, entry.Facet)
	}
	got, _ := s.Get(ctx, entry.ID)
	if len(got.Sources) == 0 || got.Sources[0].EvidenceText == "" {
		t.Fatalf("evidence text missing: %+v", got.Sources)
	}
}

func TestExtractMoodGoesToRelationshipStateOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local)
	results, err := s.ExtractTurnAt(ctx, "今天好累", "", "s1", at, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("mood produced entries: %+v", results)
	}
	rel, err := s.GetRelationship(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rel {
		if r.Key == "current_mood" {
			found = true
			if r.ExpiresAt == nil {
				t.Fatal("mood has no expiry")
			}
			want := at.Add(24 * time.Hour).UnixMilli()
			if *r.ExpiresAt != want {
				t.Fatalf("mood expiry = %d, want %d", *r.ExpiresAt, want)
			}
		}
	}
	if !found {
		t.Fatal("current_mood not written")
	}
}

func TestParseRelativeTime(t *testing.T) {
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local) // Monday
	for _, tc := range []struct {
		text      string
		precision string
		hasEvent  bool
	}{
		{"今天去了海边", PrecisionDate, true},
		{"昨天开会", PrecisionDate, true},
		{"明天出差", PrecisionPlanned, true},
		{"上周见面", PrecisionRange, false},
		{"去年夏天旅行", PrecisionApproximate, false},
		{"第一次见面", "", false},
	} {
		hint := ParseRelativeTime(tc.text, at, time.Local)
		if tc.precision == "" {
			if hint != nil {
				t.Fatalf("%q should not parse: %+v", tc.text, hint)
			}
			continue
		}
		if hint == nil {
			t.Fatalf("%q did not parse", tc.text)
		}
		if hint.Precision != tc.precision {
			t.Fatalf("%q precision = %q, want %q", tc.text, hint.Precision, tc.precision)
		}
		if tc.hasEvent && hint.EventAt == nil {
			t.Fatalf("%q missing event_at", tc.text)
		}
		if !tc.hasEvent && hint.ValidFrom == nil {
			t.Fatalf("%q missing valid_from", tc.text)
		}
	}
}
