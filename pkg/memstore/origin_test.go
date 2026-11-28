package memstore

import (
	"context"
	"testing"
	"time"
)

func TestOriginEventIdentityIsStableAndMessageScoped(t *testing.T) {
	first := BuildOriginEventID("pico", "session", "msg-1", "user")
	again := BuildOriginEventID("pico", "session", "msg-1", "user")
	second := BuildOriginEventID("pico", "session", "msg-2", "user")
	if first == "" || first != again || first == second {
		t.Fatalf("origin ids = %q, %q, %q", first, again, second)
	}
}

func TestOriginEventPersistsAndAddRequiresRecordedSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first := BuildOriginEventID("pico", "origin-session", "msg-1", "user")
	second := BuildOriginEventID("pico", "origin-session", "msg-2", "user")
	for _, eventID := range []string{first, second} {
		if err := s.RecordOriginEvent(ctx, OriginEvent{
			EventID: eventID, SourceBackend: "jsonl", SessionKey: "origin-session",
			MessageID: eventID, Role: "user", Available: true, DomainID: DomainLegacyUnassigned,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Idempotent replay of the same event is allowed.
	if err := s.RecordOriginEvent(ctx, OriginEvent{
		EventID: first, SourceBackend: "jsonl", SessionKey: "origin-session",
		MessageID: first, Role: "user", Available: true, DomainID: DomainLegacyUnassigned,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "unrecorded origin", OriginEventID: "oe_missing",
		DomainID: DomainLegacyUnassigned,
	}); err == nil {
		t.Fatal("Add accepted an origin event that was never recorded")
	}

	added, err := s.Add(ctx, AddInput{
		Text: "same fact from two messages", Layer: LayerProfile,
		SessionKey: "origin-session", SourceType: SourceExplicit,
		EvidenceText:  "same fact from two messages",
		OriginEventID: first, OriginRevision: 1,
		DomainID: DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := s.Add(ctx, AddInput{
		Text: "same fact from two messages", Layer: LayerProfile,
		SessionKey: "origin-session", SourceType: SourceExplicit,
		EvidenceText:  "same fact from two messages",
		OriginEventID: second, OriginRevision: 1,
		DomainID: DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Entry == nil || merged.Entry.ID != added.Entry.ID {
		t.Fatalf("second origin did not merge into the same fact: %+v", merged)
	}
	entry, err := s.Get(ctx, added.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.Sources) != 2 {
		t.Fatalf("sources = %+v, want two distinct origin events", entry.Sources)
	}

	if err := s.MarkOriginUnavailable(ctx, first); err != nil {
		t.Fatal(err)
	}
	entry, err = s.Get(ctx, added.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.EvidenceStatus != EvidenceSupported {
		t.Fatalf("evidence status after one retraction = %q, want supported", entry.EvidenceStatus)
	}
	if err := s.MarkOriginUnavailable(ctx, second); err != nil {
		t.Fatal(err)
	}
	entry, err = s.Get(ctx, added.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.EvidenceStatus != "unsupported" {
		t.Fatalf("evidence status after all sources retracted = %q", entry.EvidenceStatus)
	}
	if entry.Freshness != FreshnessStale {
		t.Fatalf("freshness after retraction = %q, want stale", entry.Freshness)
	}
}

func TestExtractTurnWithSourceRecordsOrigin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	eventID := BuildOriginEventID("pico", "extract-session", "msg-1", "user")
	if _, err := s.ExtractTurnWithSource(ctx, ExtractSource{
		EventID: eventID, SessionKey: "extract-session", MessageID: "msg-1",
		DomainID: DomainLegacyUnassigned,
	}, "以后叫我小原", "好的", time.Now(), false); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sources WHERE origin_event_id=?`, eventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("sources with origin = %d, want 1", count)
	}
	var available int
	if err := s.db.QueryRowContext(ctx,
		`SELECT available FROM memory_origin_events WHERE event_id=?`, eventID).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 1 {
		t.Fatal("origin event was not persisted as available")
	}
}
