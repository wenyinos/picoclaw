package memstore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCorrectCannotReviveForgottenText(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	forgotten, err := s.Add(ctx, AddInput{Text: "已被永久遗忘的句子", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, forgotten.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	live, err := s.Add(ctx, AddInput{Text: "另一条仍然存在的记忆", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Correct(ctx, live.Entry.ID, AddInput{Text: "已被永久遗忘的句子"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "blocked_forget" {
		t.Fatalf("Correct action = %q, want blocked_forget", res.Action)
	}
	current, err := s.Get(ctx, live.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Text != "另一条仍然存在的记忆" {
		t.Fatalf("Correct changed text despite tombstone: %q", current.Text)
	}
}

func TestSupersedeRejectsCrossDomainTarget(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	target, err := s.Add(ctx, AddInput{
		Text: "domain A fact", Layer: LayerProfile, DomainID: "domain:a",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Add(ctx, AddInput{
		Text: "domain B replacement", Layer: LayerProfile, DomainID: "domain:b",
		SupersedesID: target.Entry.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "across permission domains") {
		t.Fatalf("cross-domain supersede error = %v", err)
	}
}

func TestUpdateRejectsTextAndStatusBypass(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "原始正文", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, entry.Entry.ID, AddInput{Text: "绕过 Correct 的新正文"}); err == nil {
		t.Fatal("Update must reject direct text changes")
	}
	if _, err := s.Update(ctx, entry.Entry.ID, AddInput{ForceStatus: StatusArchived}); err == nil {
		t.Fatal("Update must reject direct status changes")
	}
	if _, err := s.Update(ctx, entry.Entry.ID, AddInput{PinnedCore: true}); err == nil {
		t.Fatal("Update must reject direct pinned_core promotion")
	}
}

func TestChatContextCannotPerformOwnerActions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	svc := NewMemoryService(s, DefaultConfig(t.TempDir()))
	mc := MemoryContext{
		DomainID: DomainLegacyUnassigned, ChatKind: ChatKindPrivate,
		Purpose: PurposeChat, SessionKey: "session:chat",
	}
	entry, err := s.Add(ctx, AddInput{Text: "待审核事实", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Confirm(ctx, mc, entry.Entry.ID, "model says confirmed"); !errors.Is(err, ErrOwnerApprovalRequired) {
		t.Fatalf("Confirm error = %v, want ErrOwnerApprovalRequired", err)
	}
	if err := svc.SetStatus(ctx, mc, entry.Entry.ID, StatusArchived); !errors.Is(err, ErrOwnerApprovalRequired) {
		t.Fatalf("SetStatus error = %v, want ErrOwnerApprovalRequired", err)
	}
	if err := svc.SetRelationshipState(ctx, mc, RelationshipUpdate{Key: "stage", Value: "next"}); !errors.Is(err, ErrOwnerApprovalRequired) {
		t.Fatalf("SetRelationshipState error = %v, want ErrOwnerApprovalRequired", err)
	}
	if res, err := svc.Add(ctx, mc, AddInput{Text: "模型写入的安全规则", Layer: LayerBoundary}); !errors.Is(err, ErrOwnerApprovalRequired) || res.Action != "blocked_policy" {
		t.Fatalf("boundary add = %+v, %v; want blocked policy", res, err)
	}

	pinned, err := s.Add(ctx, AddInput{Text: "核心规则", Layer: LayerProfile, PinnedCore: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Correct(ctx, mc, pinned.Entry.ID, AddInput{Text: "模型强改核心", Force: true}); !errors.Is(err, ErrOwnerApprovalRequired) {
		t.Fatalf("forced core correct error = %v, want ErrOwnerApprovalRequired", err)
	}
	if _, err := svc.Correct(ctx, mc, entry.Entry.ID, AddInput{Text: "模型改写", Layer: LayerBoundary}); !errors.Is(err, ErrOwnerApprovalRequired) {
		t.Fatalf("boundary correct error = %v, want ErrOwnerApprovalRequired", err)
	}
}

func TestRelationshipStateValidatesSecretsAndLength(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	svc := NewMemoryService(s, DefaultConfig(t.TempDir()))
	owner := MemoryContext{
		DomainID: DomainLegacyUnassigned, ChatKind: ChatKindPrivate, Purpose: "dashboard",
	}
	if err := svc.SetRelationshipState(ctx, owner, RelationshipUpdate{
		Key: "stage", Value: "my password is hunter2",
	}); err == nil {
		t.Fatal("relationship state must reject credentials")
	}
	if err := svc.SetRelationshipState(ctx, owner, RelationshipUpdate{
		Key: "stage", Value: strings.Repeat("长", maxRelationshipValueRunes+1),
	}); err == nil {
		t.Fatal("relationship state must reject oversized values")
	}
}

func TestSecretClassifierCoversEnglishCredentials(t *testing.T) {
	for _, text := range []string{
		"my password is hunter2",
		"Authorization: Bearer abcdefghijklmnop",
		"api key sk-abcdefghijklmnopqrstuvwxyz123456",
		"token ghp_abcdefghijklmnopqrstuvwxyz1234",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
	} {
		if got := SensitivityForText(text); got != SensitivitySecret {
			t.Errorf("SensitivityForText(%q) = %q, want secret", text, got)
		}
	}
}
