package memstore

import (
	"context"
	"fmt"
	"testing"
)

func TestReconcilePendingIntentReplaysApprovedManifest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "approved pending target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	ledger := s.ForgetLedgerRef()
	digest, keyID, version := ledger.Digest(entry.Entry.Text)
	manifest := ForgetTargetManifest{EntryIDs: []string{entry.Entry.ID}}
	intent, err := ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "approved-request", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest,
		HashVersion: version, KeyID: keyID,
		ScopeJSON: fmt.Sprintf(`{"kind":"entry","actor":"dashboard@test","expected_revision":%d}`,
			entry.Entry.RowRevision),
		TargetManifest: MarshalManifest(manifest),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.ReconcilePendingIntents(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Replayed != 1 || report.Blocked != 0 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := s.Get(ctx, entry.Entry.ID); err == nil {
		t.Fatal("approved pending target still present")
	}
	var jobCount, opCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_jobs WHERE job_id=?`, "forget:approved-request").Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_operations WHERE request_id='approved-request'`).Scan(&opCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 || opCount != 1 {
		t.Fatalf("cleanup/op rows = %d/%d, want 1/1", jobCount, opCount)
	}
	pending, err := ledger.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range pending {
		if in.Seq == intent.Seq {
			t.Fatal("reconciled intent remained pending")
		}
	}
}

func TestReconcilePendingIntentDoesNotReplayUnapproved(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "unapproved pending target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	ledger := s.ForgetLedgerRef()
	digest, keyID, version := ledger.Digest(entry.Entry.Text)
	if _, err := ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "unapproved-request", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest,
		HashVersion: version, KeyID: keyID,
		ScopeJSON:      `{"kind":"entry"}`,
		TargetManifest: MarshalManifest(ForgetTargetManifest{EntryIDs: []string{entry.Entry.ID}}),
	}); err != nil {
		t.Fatal(err)
	}
	report, err := s.ReconcilePendingIntents(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.SkippedUnapproved != 1 || report.Replayed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := s.Get(ctx, entry.Entry.ID); err != nil {
		t.Fatal("unapproved target was deleted")
	}
}

func TestReconcilePendingIntentBlocksChangedRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "changed approved target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	originalRevision := entry.Entry.RowRevision
	if _, err := s.Update(ctx, entry.Entry.ID, AddInput{Importance: 0.95}); err != nil {
		t.Fatal(err)
	}
	ledger := s.ForgetLedgerRef()
	digest, keyID, version := ledger.Digest(entry.Entry.Text)
	if _, err := ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "revision-conflict", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest,
		HashVersion: version, KeyID: keyID,
		ScopeJSON:      fmt.Sprintf(`{"actor":"dashboard@test","expected_revision":%d}`, originalRevision),
		TargetManifest: MarshalManifest(ForgetTargetManifest{EntryIDs: []string{entry.Entry.ID}}),
	}); err != nil {
		t.Fatal(err)
	}
	report, err := s.ReconcilePendingIntents(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Blocked != 1 || report.Replayed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := s.Get(ctx, entry.Entry.ID); err != nil {
		t.Fatal("newer corrected entry was deleted by stale approval")
	}
}
