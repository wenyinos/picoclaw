package memstore

import (
	"context"
	"strings"
	"testing"
)

func TestForgetCascadesAndBlocksResurrection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	added, err := s.Add(ctx, AddInput{
		Text: "用户不希望保留这条记忆", Layer: LayerProfile, Subject: "user",
		Predicate: "偏好", Facet: "隐私", Importance: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := added.Entry.ID

	// A relation and a capsule membership that must disappear with the entry.
	other, err := s.Add(ctx, AddInput{Text: "另一条相关记忆", Layer: LayerProcedural})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddRelation(ctx, Relation{
		SourceID: id, TargetID: other.Entry.ID, RelationType: RelRelatedTo,
	}); err != nil {
		t.Fatal(err)
	}
	now := nowMillis()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_clusters(id,realm,title,topic,status,created_at,updated_at)
		 VALUES('test:capsule','real','测试','t','active',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_cluster_members(cluster_id,entry_id,ordinal) VALUES('test:capsule',?,1)`,
		id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_summary_versions(cluster_id,version,summary,created_at)
		 VALUES('test:capsule',1,'摘要',?)`, now); err != nil {
		t.Fatal(err)
	}

	preview, err := s.Forget(ctx, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Sources != 1 || preview.Relations != 1 || preview.Summaries != 1 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if _, err := s.Get(ctx, id); err != nil {
		t.Fatalf("dry run deleted the entry: %v", err)
	}

	if _, err := s.Forget(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); err == nil {
		t.Fatal("entry still exists after forget")
	}
	var sources, relations, members int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sources WHERE entry_id=?`, id).Scan(&sources)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_relations WHERE source_id=? OR target_id=?`, id, id).Scan(&relations)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_cluster_members WHERE entry_id=?`, id).Scan(&members)
	if sources != 0 || relations != 0 || members != 0 {
		t.Fatalf("derived rows left: sources=%d relations=%d members=%d", sources, relations, members)
	}
	var summaryStatus string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM memory_summary_versions WHERE cluster_id='test:capsule'`).Scan(&summaryStatus); err != nil {
		t.Fatal(err)
	}
	if summaryStatus != "invalidated" {
		t.Fatalf("summary status = %q, want invalidated", summaryStatus)
	}

	// The operation log keeps a hash, never the text.
	var hash, reason string
	if err := s.db.QueryRowContext(ctx,
		`SELECT content_hash,reason FROM memory_operations WHERE operation='forget'`).Scan(&hash, &reason); err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("forget tombstone has no content hash")
	}
	if strings.Contains(reason, "不希望保留") {
		t.Fatal("operation log leaked memory text")
	}

	// Re-extracting the same sentence is blocked.
	res, err := s.Add(ctx, AddInput{Text: "用户不希望保留这条记忆", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "blocked_forget" {
		t.Fatalf("resurrection action = %q, want blocked_forget", res.Action)
	}

	// ReplayForget removes a restored copy.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO entries (`+entryColumns+`) VALUES (`+entryPlaceholders+`)`,
		"restored", "real", "profile", "user", "all", "user", "偏好", "隐私", "",
		"用户不希望保留这条记忆", Normalize("用户不希望保留这条记忆"), hash, int64(0), "", "",
		1, 0.9, 0.0, 0.0, 0.8, "active", nil, nil, now, now, nil, 0, "{}",
		PrecisionUnknown, "", "user_stated", nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	removed, err := s.ReplayForget(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("replay removed %d, want 1", removed)
	}
}

func TestCorrectKeepsHistoryAndPinnedGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "旧的名字是小明", Layer: LayerProfile, Subject: "user",
		Predicate: "nickname", Facet: "称呼", Value: "小明", PinnedCore: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !added.Entry.PinnedCore {
		t.Fatal("pinned_core was not stored")
	}
	// Automatic supersede is blocked and waits for review.
	res, err := s.Add(ctx, AddInput{
		Text: "新的名字是阿明", Layer: LayerProfile, Subject: "user",
		Predicate: "nickname", Facet: "称呼", Value: "阿明",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Action != "pending_review" || res.Entry.Status != StatusPending {
		t.Fatalf("pinned conflict = %+v", res)
	}

	// Correct without force refuses.
	if _, err := s.Correct(ctx, added.Entry.ID, AddInput{Text: "强制修正"}); err == nil {
		t.Fatal("correct without force should fail on a pinned entry")
	}
	corrected, err := s.Correct(ctx, added.Entry.ID, AddInput{Text: "强制修正", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.Get(ctx, added.Entry.ID)
	if old.Status != StatusSuperseded || old.ValidTo == nil {
		t.Fatalf("old entry not closed: %+v", old)
	}
	if corrected.Entry == nil || corrected.Entry.Status != StatusActive {
		t.Fatalf("replacement not active: %+v", corrected)
	}
	ops, err := s.ListOperations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range ops {
		if op.Operation == "correct" && op.TargetEntryID == added.Entry.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("correct operation not logged: %+v", ops)
	}
}
