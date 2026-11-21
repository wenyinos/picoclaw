package memstore

import (
	"context"
	"testing"
)

type fakeCleaner struct {
	calls int
	locs  []SourceLocator
}

func (f *fakeCleaner) CleanLocators(_ context.Context, locators []SourceLocator) (CleanupOutcome, error) {
	f.calls++
	f.locs = append(f.locs, locators...)
	return CleanupOutcome{Removed: 1, Sessions: 1}, nil
}

// TestForgetQueuesCleanupJob verifies the cross-store obligation is committed
// with the deletion and drained by RunCleanupJobs.
func TestForgetQueuesCleanupJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	added, err := s.Add(ctx, AddInput{
		Text: "这句话之后会被遗忘", Layer: LayerProfile,
		SessionKey: "sess-1", SourceType: SourceExplicit,
		EvidenceText: "这句话之后会被遗忘",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}

	jobs, err := s.ListJobs(ctx, JobPending, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Kind != "forget_cleanup" {
		t.Fatalf("cleanup job missing: %+v", jobs)
	}

	cleaner := &fakeCleaner{}
	outcome, err := s.RunCleanupJobs(ctx, cleaner, 5)
	if err != nil {
		t.Fatal(err)
	}
	if cleaner.calls != 1 {
		t.Fatalf("cleaner calls = %d, want 1", cleaner.calls)
	}
	if len(cleaner.locs) != 1 || cleaner.locs[0].SessionKey != "sess-1" {
		t.Fatalf("locators = %+v", cleaner.locs)
	}
	if cleaner.locs[0].EvidenceHMAC == "" {
		t.Fatal("locator has no evidence HMAC")
	}
	if outcome.Removed != 1 || outcome.Sessions != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	// The job is done, not left pending.
	pending, _ := s.ListJobs(ctx, JobPending, 10)
	if len(pending) != 0 {
		t.Fatalf("job not completed: %+v", pending)
	}
}

// TestCleanupJobWithoutLocatorReportsPartial verifies the honest boundary:
// a forget with nothing locatable completes with a partial note, never a
// fake "removed" count.
func TestCleanupJobWithoutLocatorReportsPartial(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{Text: "没有会话来源的记忆", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	cleaner := &fakeCleaner{}
	outcome, err := s.RunCleanupJobs(ctx, cleaner, 5)
	if err != nil {
		t.Fatal(err)
	}
	if cleaner.calls != 0 {
		t.Fatal("cleaner must not be called without locators")
	}
	if outcome.Removed != 0 || len(outcome.Details) == 0 {
		t.Fatalf("expected partial note, got %+v", outcome)
	}
}
