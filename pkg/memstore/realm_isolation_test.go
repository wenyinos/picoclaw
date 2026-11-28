package memstore

import (
	"context"
	"errors"
	"testing"
)

func TestGroupChatRealmCannotBeWidened(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, in := range []AddInput{
		{Text: "REAL_SHARED 公共日程", Realm: RealmReal, Layer: LayerEpisodic, Audience: "all"},
		{Text: "FICTIONAL_SHARED 角色设定", Realm: RealmFictional, Layer: LayerEpisodic, Audience: "all"},
		{Text: "WORK_SHARED 工作流程", Realm: RealmWork, Layer: LayerProcedural, Audience: "all"},
	} {
		if _, err := s.Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewMemoryService(s, DefaultConfig(""))
	group := MemoryContext{
		DomainID: DomainLegacyUnassigned, ChatKind: ChatKindGroup,
		Channel: "pico", Purpose: "chat",
	}

	got, err := svc.Search(ctx, group, SearchOptions{Query: "SHARED", AllLayers: true, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Realm != RealmReal {
		t.Fatalf("group default search = %+v, want only real", got)
	}

	for _, realm := range []string{RealmFictional, RealmWork} {
		_, err := svc.Search(ctx, group, SearchOptions{
			Query: "SHARED", Realm: realm, AllLayers: true, Limit: 20,
		})
		if !errors.Is(err, ErrRealmNotAllowed) {
			t.Fatalf("group realm %q error = %v, want ErrRealmNotAllowed", realm, err)
		}
	}

	// Low-level Store callers cannot widen the scope either: the SQL predicate
	// intersects the requested realm with the shared-chat restriction.
	for _, realm := range []string{RealmFictional, RealmWork} {
		got, err := s.Search(ctx, SearchOptions{
			Query: "SHARED", Realm: realm, ChatKindHint: ChatKindGroup,
			Channel: "pico", AllLayers: true, Limit: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("direct group search realm=%q leaked %d entries", realm, len(got))
		}
	}

	fictional, err := s.SearchEntries(ctx, SearchOptions{
		Query: "FICTIONAL_SHARED", Realm: RealmFictional, Limit: 1,
	})
	if err != nil || len(fictional) != 1 {
		t.Fatalf("test fixture fictional entry missing: %v %d", err, len(fictional))
	}
	if _, err := svc.Get(ctx, group, fictional[0].ID); err == nil {
		t.Fatal("group inspect returned a fictional entry")
	}
}

func TestPrivateChatRealmFilterStillWorks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{
		Text: "PRIVATE_FICTIONAL 私有角色", Realm: RealmFictional,
		Layer: LayerEpisodic, Audience: "private",
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewMemoryService(s, DefaultConfig(""))
	got, err := svc.Search(ctx, MemoryContext{
		DomainID: DomainLegacyUnassigned, ChatKind: ChatKindPrivate,
		Channel: "pico", Purpose: "chat",
	}, SearchOptions{
		Query: "PRIVATE_FICTIONAL", Realm: RealmFictional, AllLayers: true,
		IncludeFic: true, Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("private fictional results = %d, want 1", len(got))
	}
}
