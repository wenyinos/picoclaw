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

func TestRepairDataRestoresOnlyMissingDefaultDomain(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_domains WHERE domain_id=?`, DomainLegacyUnassigned); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_domains(domain_id,owner_principal_id,scope_key,status,policy_epoch,created_at,updated_at)
		 VALUES('kept_domain','owner-kept','scope','active',7,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepairData(ctx); err != nil {
		t.Fatal(err)
	}
	var owner string
	var epoch int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT owner_principal_id,policy_epoch FROM memory_domains WHERE domain_id=?`,
		DomainLegacyUnassigned).Scan(&owner, &epoch); err != nil {
		t.Fatal(err)
	}
	if owner != "" || epoch != 1 {
		t.Fatalf("repaired default domain owner/epoch = %q/%d", owner, epoch)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT owner_principal_id FROM memory_domains WHERE domain_id='kept_domain'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "owner-kept" {
		t.Fatalf("unrelated domain was modified: %q", owner)
	}
	status, err := s.RecoveryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.DomainRegistryOK {
		t.Fatalf("domain registry still unhealthy: %+v", status)
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

type outcomeUnknownCleaner struct{}

func (c *outcomeUnknownCleaner) CleanLocators(_ context.Context, locators []SourceLocator) (CleanupOutcome, error) {
	out := CleanupOutcome{OutcomeUnknown: len(locators)}
	for _, loc := range locators {
		out.Receipts = append(out.Receipts, LocatorReceipt{
			Backend: cleanupBackendForLocator(loc), LocatorKey: LocatorReceiptKey(loc),
			State: ReceiptOutcomeUnknown, ReasonCode: "test_unknown",
		})
	}
	return out, nil
}

func TestCleanupOutcomeUnknownStaysBlocked(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "outcome unknown cleanup target", Layer: LayerProfile,
		SessionKey: "unknown-session", SourceType: SourceExplicit,
		EvidenceText: "outcome unknown cleanup target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	outcome, err := s.RunCleanupJobs(ctx, &outcomeUnknownCleaner{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.OutcomeUnknown == 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	blocked, err := s.ListJobs(ctx, JobBlocked, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 1 {
		t.Fatalf("blocked jobs = %+v", blocked)
	}
	intents, err := s.ledger.AllIntents(ctx)
	if err != nil || len(intents) != 1 {
		t.Fatalf("intents = %+v, err=%v", intents, err)
	}
	receipts, err := s.ledger.ReceiptsForIntent(ctx, intents[0].Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].State != ReceiptOutcomeUnknown {
		t.Fatalf("receipts = %+v", receipts)
	}
}

func TestCleanupRecoversPriorRunningReceiptAsVerifiedAbsent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "previous run cleanup target", Layer: LayerProfile,
		SessionKey: "prior-run-session", SourceType: SourceExplicit,
		EvidenceText: "previous run cleanup target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(ctx, JobPending, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v, err=%v", jobs, err)
	}
	manifest, err := ParseManifest(jobs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := s.ledger.IntentByRequestID(ctx, jobs[0].InputFingerprint)
	if err != nil || intent == nil {
		t.Fatalf("intent lookup: %+v %v", intent, err)
	}
	running := make([]LocatorReceipt, 0, len(manifest.Locators))
	for _, loc := range manifest.Locators {
		running = append(running, LocatorReceipt{
			Backend: cleanupBackendForLocator(loc), LocatorKey: LocatorReceiptKey(loc),
			State: ReceiptRunning, ReasonCode: "cleanup_started",
		})
	}
	if err := s.ledger.SaveReceipts(ctx, *intent, running, 1); err != nil {
		t.Fatal(err)
	}
	outcome, err := s.RunCleanupJobs(ctx, &rootNoMatchCleaner{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Unavailable != 0 || outcome.AlreadyAbsent != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	done, err := s.ListJobs(ctx, JobDone, 10)
	if err != nil || len(done) != 1 {
		t.Fatalf("done jobs = %+v, err=%v", done, err)
	}
	receipts, err := s.ledger.ReceiptsForIntent(ctx, intent.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].State != ReceiptVerifiedAbsent {
		t.Fatalf("receipts = %+v", receipts)
	}
}

type rootNoMatchCleaner struct{}

func (c *rootNoMatchCleaner) CleanLocators(_ context.Context, locators []SourceLocator) (CleanupOutcome, error) {
	out := CleanupOutcome{Unavailable: len(locators)}
	for _, loc := range locators {
		out.Receipts = append(out.Receipts, LocatorReceipt{
			Backend: cleanupBackendForLocator(loc), LocatorKey: LocatorReceiptKey(loc),
			State: ReceiptUnavailable, ReasonCode: "root_no_match",
		})
	}
	return out, nil
}
