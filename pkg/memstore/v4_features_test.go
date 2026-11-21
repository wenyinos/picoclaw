package memstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestDomainIsolationSameText verifies FR-03/B03: identical text in two
// permission partitions stays independent and dedupe never crosses domains.
func TestDomainIsolationSameText(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.Add(ctx, AddInput{
		Text: "用户喜欢咖啡", Layer: LayerProfile, Subject: "user",
		Predicate: "偏好", Facet: "饮品", DomainID: "domain:a",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add(ctx, AddInput{
		Text: "用户喜欢咖啡", Layer: LayerProfile, Subject: "user",
		Predicate: "偏好", Facet: "饮品", DomainID: "domain:b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Entry.ID == second.Entry.ID {
		t.Fatalf("cross-domain dedupe merged %s and %s", first.Entry.ID, second.Entry.ID)
	}
	if second.Action != "added" {
		t.Fatalf("second domain action = %q, want added", second.Action)
	}
	// Same domain still dedupes.
	third, err := s.Add(ctx, AddInput{
		Text: "用户喜欢咖啡", Layer: LayerProfile, Subject: "user",
		Predicate: "偏好", Facet: "饮品", DomainID: "domain:a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Action != "duplicate" || third.ExistingID != first.Entry.ID {
		t.Fatalf("in-domain dedupe broken: %+v", third)
	}
}

// TestForgetRemovesRelationshipCopy verifies B06: forgetting the nickname
// memory also removes the relationship-state copy that feeds the prompt block.
func TestForgetRemovesRelationshipCopy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	results, err := s.ExtractTurnAt(ctx, "以后叫我阿泽", "s1", "s1", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("nickname extraction produced no entry")
	}
	entryID := results[0].Entry.ID
	rel, err := s.GetRelationship(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rel {
		if r.Key == "user_name" && r.Value == "阿泽" {
			found = true
		}
	}
	if !found {
		t.Fatalf("nickname did not reach relationship state: %+v", rel)
	}

	preview, err := s.Forget(ctx, entryID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.RelationshipKeys) == 0 {
		t.Fatal("forget preview does not list relationship copies")
	}
	if _, err := s.Forget(ctx, entryID, false); err != nil {
		t.Fatal(err)
	}
	rel, err = s.GetRelationship(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rel {
		if r.Key == "user_name" {
			t.Fatalf("relationship copy survived forget: %+v", r)
		}
	}
}

// TestLedgerReplaysAcrossRestore verifies G2/FR-31: the independent ledger
// still blocks and removes content after the main database loses the tombstone.
func TestLedgerReplaysAcrossRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{Text: "这条稍后要被遗忘", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	// Simulate a restored old backup: the entry is back, the operation-log
	// tombstone is gone, but the independent ledger is intact.
	restored, err := s.Add(ctx, AddInput{
		Text: "这条稍后要被遗忘", Layer: LayerProfile, BypassForgetTombstone: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Action != "added" {
		t.Fatalf("bypass add action = %q", restored.Action)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_operations WHERE operation='forget'`); err != nil {
		t.Fatal(err)
	}
	// Re-learning is still blocked by the ledger, not by the main database.
	blocked, err := s.Add(ctx, AddInput{Text: "这条稍后要被遗忘", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Action != "blocked_forget" {
		t.Fatalf("ledger tombstone did not block re-learning: %+v", blocked)
	}
	removed, err := s.ReplayForget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("ledger replay removed %d, want 1", removed)
	}
}

// TestChangePlanRevisionConflict verifies FR-05/§6.5: an approved plan refuses
// to overwrite a target that changed after the preview.
func TestChangePlanRevisionConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{Text: "旧内容", Layer: LayerProfile, DomainID: DomainLegacyUnassigned})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewMemoryService(s, Config{})
	owner := MemoryContext{DomainID: DomainLegacyUnassigned, ChatKind: ChatKindAdmin}
	plan, err := svc.CreateChangePlan(ctx, owner, "correct", added.Entry.ID, AddInput{Text: "新内容"})
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent metadata update bumps row_revision.
	if _, err := s.Update(ctx, added.Entry.ID, AddInput{Importance: 0.9}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveChangePlan(ctx, owner, plan.PlanID, "tester"); err == nil {
		t.Fatal("stale plan must fail with a revision conflict")
	}
}

// TestSummaryRequiresEvidenceAndInvalidates verifies G5: summaries need live
// evidence per claim and become invisible when a member is forgotten.
func TestSummaryRequiresEvidenceAndInvalidates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.Add(ctx, AddInput{Text: "一起去看海", Layer: LayerEpisodic, Importance: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add(ctx, AddInput{Text: "一起过生日", Layer: LayerEpisodic, Importance: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := s.CreateCluster(ctx, Cluster{Title: "第一阶段", Realm: RealmReal})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddClusterMembers(ctx, cluster.ID, []string{first.Entry.ID, second.Entry.ID}); err != nil {
		t.Fatal(err)
	}
	version, err := s.GenerateSummary(ctx, cluster.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(version.Claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(version.Claims))
	}
	evidence, err := s.ExpandSummaryEvidence(ctx, version.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence = %d, want 2", len(evidence))
	}
	if _, err := s.PublishSummary(ctx, cluster.ID, "无证据摘要", []SummaryClaim{
		{ClaimID: "x", Text: "无依据", EntryIDs: []string{"missing"}},
	}, "test", "v1"); err == nil {
		t.Fatal("summary without live evidence must be rejected")
	}
	if _, err := s.Forget(ctx, first.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSummaries(ctx, cluster.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	invalidated := 0
	for _, v := range list {
		if v.Status == "invalidated" {
			invalidated++
		}
	}
	if invalidated == 0 {
		t.Fatalf("summary not invalidated after forget: %+v", list)
	}
}

// TestCommitmentLifecycle verifies G6: due dates never auto-fulfill and
// closing a promise needs explicit evidence.
func TestCommitmentLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour).UnixMilli()
	c, err := s.CreateCommitment(ctx, Commitment{Text: "周五提醒我打电话", DueAt: &due})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateCommitmentStatus(ctx, c.ID, CommitmentFulfilled, ""); err == nil {
		t.Fatal("fulfilled without evidence must be rejected")
	}
	updated, err := s.UpdateCommitmentStatus(ctx, c.ID, CommitmentFulfilled, "用户说已经打过了")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != CommitmentFulfilled || updated.Revision != 2 {
		t.Fatalf("commitment = %+v", updated)
	}
	if _, err := s.UpdateCommitmentStatus(ctx, c.ID, CommitmentConfirmed, "x"); err == nil {
		t.Fatal("closed commitment must not transition again")
	}
}

// TestJobLeaseAndRetry verifies G2's durable job basics: lease, fail, block.
func TestJobLeaseAndRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnqueueJob(ctx, Job{JobID: "job-1", Kind: "forget_cleanup", Payload: `{"entry":"x"}`}); err != nil {
		t.Fatal(err)
	}
	// Duplicate enqueue is a no-op, not a second job.
	if err := s.EnqueueJob(ctx, Job{JobID: "job-1", Kind: "forget_cleanup"}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListJobs(ctx, JobPending, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	leased, err := s.LeaseJob(ctx, "worker-1", time.Minute)
	if err != nil || leased == nil {
		t.Fatalf("lease failed: %v", err)
	}
	if leased.State != JobRunning || leased.Attempts != 1 {
		t.Fatalf("leased job = %+v", leased)
	}
	if err := s.FailJob(ctx, leased.JobID, "transient", 10*time.Millisecond, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.FailJob(ctx, leased.JobID, "again", 10*time.Millisecond, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.ListJobs(ctx, JobBlocked, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("blocked jobs = %d, want 1", len(jobs))
	}
}

// TestVisibilityBlocksPrivateInGroup verifies §5.2: a group chat cannot read
// private memory even on a channel configured as private.
func TestVisibilityBlocksPrivateInGroup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{
		Text: "私密偏好", Layer: LayerProfile, Audience: "private",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "公开偏好", Layer: LayerProfile, Audience: "all",
	}); err != nil {
		t.Fatal(err)
	}
	group, err := s.Search(ctx, SearchOptions{
		Query: "偏好", ChatKindHint: ChatKindGroup, Channel: "pico",
		PrivateChannels: []string{"pico"}, AllLayers: true, DomainID: DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range group {
		if e.Audience == "private" {
			t.Fatalf("group chat read private memory: %+v", e)
		}
	}
}

// TestSensitivityClassifier verifies FR-12: credentials never get stored and
// health material is escalated.
func TestSensitivityClassifier(t *testing.T) {
	if got := SensitivityForText("我的密码是 abc123"); got != SensitivitySecret {
		t.Fatalf("password classified as %q", got)
	}
	if got := SensitivityForText("我确诊了糖尿病"); got != SensitivitySensitive {
		t.Fatalf("health classified as %q", got)
	}
	if got := SensitivityForText("我喜欢散步"); got != SensitivityNormal {
		t.Fatalf("normal text classified as %q", got)
	}
	s := newTestStore(t)
	svc := NewMemoryService(s, Config{})
	_, err := svc.Add(context.Background(), MemoryContext{
		DomainID: DomainLegacyUnassigned, ChatKind: ChatKindPrivate,
	}, AddInput{Text: "我的密码是 abc123", Layer: LayerProfile})
	if err == nil {
		t.Fatal("secret write must be rejected")
	}
}

// TestExpiredEntriesNotInjected verifies B08's TTL unification for the fixed
// context path: an expired profile entry is not rendered.
func TestFixedContextSkipsExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := s.Add(ctx, AddInput{
		Text: "已经过期的画像", Layer: LayerProfile, Audience: "all", ExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	text, _ := s.BuildFixedContext(ctx, 600, false, "", nil)
	if containsString(text, "已经过期的画像") {
		t.Fatalf("expired entry leaked into the fixed block: %q", text)
	}
}

func containsString(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
