package memstore

import (
	"context"
	"testing"
)

func TestApplyUpgradeCompletesLegacyDomainAndCleanupBlockers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entry, err := s.Add(ctx, AddInput{
		Text: "legacy upgrade cleanup target", Layer: LayerProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, entry.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	// Simulate the historical state seen in real upgrades: the cleanup
	// projection was cancelled outside the current state machine.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_jobs SET state='cancelled' WHERE job_kind='forget_cleanup'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_domains WHERE domain_id=?`, DomainLegacyUnassigned); err != nil {
		t.Fatal(err)
	}

	preview, err := s.PreviewUpgrade(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.CanComplete || !preview.DefaultDomainMissing ||
		len(preview.LegacyCleanupJobs) != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Recovery.Ready {
		t.Fatal("preview unexpectedly reported ready")
	}

	report, err := s.ApplyUpgrade(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.ClosedLegacyJobs != 1 || !report.ResidualRisk {
		t.Fatalf("report = %+v", report)
	}
	if report.Backup == "" {
		t.Fatal("one-time upgrade did not report a backup")
	}
	if report.Recovery.LegacyClosed != 1 {
		t.Fatalf("recovery legacy_closed = %d", report.Recovery.LegacyClosed)
	}
	var state string
	if err := s.db.QueryRowContext(ctx,
		`SELECT state FROM memory_jobs WHERE job_kind='forget_cleanup' LIMIT 1`).
		Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != JobLegacyClosed {
		t.Fatalf("legacy cleanup state = %q, want %q", state, JobLegacyClosed)
	}
	var domainCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_domains WHERE domain_id=?`,
		DomainLegacyUnassigned).Scan(&domainCount); err != nil {
		t.Fatal(err)
	}
	if domainCount != 1 {
		t.Fatalf("default domain count = %d", domainCount)
	}
}

func TestUpgradePreviewRefusesUnresolvedForgetIntents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{
		Text: "unapproved upgrade intent target", Layer: LayerProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := s.ledger.Digest(entry.Entry.Text)
	if _, err := s.ledger.RecordIntent(ctx, ForgetIntent{
		RequestID: "upgrade-unapproved", DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest, KeyID: keyID,
		TargetManifest: MarshalManifest(ForgetTargetManifest{
			EntryIDs: []string{entry.Entry.ID},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewUpgrade(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.CanComplete {
		t.Fatalf("preview must not complete with pending intents: %+v", preview)
	}
	found := false
	for _, reason := range preview.BlockingReasons {
		if reason == "pending_forget_intents" {
			found = true
		}
	}
	if !found {
		t.Fatalf("blocking reasons = %+v", preview.BlockingReasons)
	}
}
