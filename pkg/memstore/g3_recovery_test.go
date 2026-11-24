package memstore

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestLedgerConcurrentFirstOpenKeepsOneIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forget-ledger.db")
	const workers = 6
	ids := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ledger, err := OpenForgetLedger(path)
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = ledger.UUID()
			_ = ledger.Close()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	for i := 1; i < workers; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("identity mismatch: %q != %q", ids[i], ids[0])
		}
	}
	ledger, err := OpenExistingForgetLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if ledger.UUID() != ids[0] {
		t.Fatalf("persisted identity = %q, want %q", ledger.UUID(), ids[0])
	}
}

func TestHistoricalForgetAuditIsReportOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "historical audit target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, entry.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	report, err := s.AuditHistoricalForgets(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.TargetsGone != 1 || report.TargetsPresent != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Items) != 1 || report.Items[0].TargetID != entry.Entry.ID {
		t.Fatalf("audit items = %+v", report.Items)
	}
}

func TestRecordIntentIsIdempotentByRequestID(t *testing.T) {
	ledger, err := OpenForgetLedger(filepath.Join(t.TempDir(), "forget-ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	ctx := context.Background()
	first, err := ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "request-1", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: "digest-a",
		TargetManifest: `{"entry_ids":["e1"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "request-1", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: "digest-a",
		TargetManifest: `{"entry_ids":["e1"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != first.Seq {
		t.Fatalf("idempotent request created seq %d then %d", first.Seq, second.Seq)
	}
	if _, err := ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "request-1", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: "different",
		TargetManifest: `{"entry_ids":["e2"]}`,
	}); err == nil {
		t.Fatal("reusing request_id with different content must conflict")
	}
	var count int
	if err := ledger.db.QueryRow(`SELECT COUNT(*) FROM forget_journal WHERE request_id='request-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("journal rows = %d, want 1", count)
	}
}
