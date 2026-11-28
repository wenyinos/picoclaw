package memstore

import (
	"context"
	"errors"
	"testing"
)

func TestCleanupPersistsReceiptsAndUnblocksMemory(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "receipt cleanup target", Layer: LayerProfile,
		SessionKey: "receipt-session", SourceType: SourceExplicit,
		EvidenceText: "receipt cleanup target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	status, err := s.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.PendingCleanup != 1 {
		t.Fatalf("status after forget = %+v", status)
	}
	if _, err := s.PreviewForgetReplay(ctx); err != nil {
		t.Fatal(err)
	}
	svc := NewMemoryService(s, DefaultConfig(""))
	if _, err := svc.Search(ctx, MemoryContext{
		DomainID: DomainLegacyUnassigned, ChatKind: ChatKindAdmin, Purpose: "maintenance",
	}, SearchOptions{Query: "receipt"}); !errors.Is(err, ErrMemoryNotReady) {
		t.Fatalf("service search during recovery error = %v, want ErrMemoryNotReady", err)
	}

	cleaner := &fakeCleaner{}
	if _, err := s.RunCleanupJobs(ctx, cleaner, 5); err != nil {
		t.Fatal(err)
	}
	status, err = s.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready {
		t.Fatalf("status after cleanup = %+v", status)
	}
	intents, err := s.ledger.AllIntents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(intents) != 1 {
		t.Fatalf("intents = %d, want 1", len(intents))
	}
	receipts, err := s.ledger.ReceiptsForIntent(ctx, intents[0].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].State != ReceiptRemoved || receipts[0].Backend == "" {
		t.Fatalf("receipts = %+v", receipts)
	}
}

func TestRecoveryBlockedByMissingDefaultDomain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_domains WHERE domain_id=?`, DomainLegacyUnassigned); err != nil {
		t.Fatal(err)
	}
	status, err := s.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.DomainRegistryOK {
		t.Fatalf("status = %+v", status)
	}
	found := false
	for _, issue := range status.Issues {
		if issue == "default_domain_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %+v", status.Issues)
	}
}

func TestRequeueBlockedCleanupCreatesSuccessor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "requeue cleanup target", Layer: LayerProfile,
		SessionKey: "requeue-session", SourceType: SourceExplicit,
		EvidenceText: "requeue cleanup target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	blockedCleaner := &unavailableCleaner{}
	if _, err := s.RunCleanupJobs(ctx, blockedCleaner, 5); err != nil {
		t.Fatal(err)
	}
	blockedJobs, err := s.ListJobs(ctx, JobBlocked, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockedJobs) != 1 {
		t.Fatalf("blocked jobs = %+v", blockedJobs)
	}
	successor, err := s.RequeueJob(ctx, blockedJobs[0].JobID)
	if err != nil {
		t.Fatal(err)
	}
	if successor.State != JobPending || successor.JobID == blockedJobs[0].JobID {
		t.Fatalf("successor = %+v", successor)
	}
	oldJobs, err := s.ListJobs(ctx, JobSuperseded, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldJobs) != 1 {
		t.Fatalf("superseded jobs = %+v", oldJobs)
	}
	status, err := s.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.PendingCleanup != 1 || status.BlockedCleanup != 0 {
		t.Fatalf("status after requeue = %+v", status)
	}
	if _, err := s.RunCleanupJobs(ctx, &fakeCleaner{}, 5); err != nil {
		t.Fatal(err)
	}
	status, err = s.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready {
		t.Fatalf("status after successor cleanup = %+v", status)
	}
}

type unavailableCleaner struct{}

func (c *unavailableCleaner) CleanLocators(_ context.Context, locators []SourceLocator) (CleanupOutcome, error) {
	out := CleanupOutcome{Unavailable: len(locators)}
	for _, loc := range locators {
		out.Receipts = append(out.Receipts, LocatorReceipt{
			Backend: cleanupBackendForLocator(loc), LocatorKey: LocatorReceiptKey(loc),
			State: ReceiptUnavailable, ReasonCode: "test_unavailable",
		})
		out.Details = append(out.Details, "test unavailable")
	}
	return out, nil
}
