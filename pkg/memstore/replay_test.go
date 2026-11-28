package memstore

import (
	"context"
	"fmt"
	"testing"
)

func recordReplayIntent(t *testing.T, s *Store, requestID string, manifest ForgetTargetManifest,
	scope string, digest string, keyID string) ForgetIntent {
	t.Helper()
	intent, err := s.ledger.RecordIntent(context.Background(), ForgetIntent{
		RequestID: requestID, DomainID: DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest,
		HashVersion: "hmac-sha256-v1", KeyID: keyID,
		ScopeJSON: scope, TargetManifest: MarshalManifest(manifest),
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestForgetReplayNeverExecutesBlockedIntent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "blocked replay target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := s.ledger.Digest(entry.Entry.Text)
	intent := recordReplayIntent(t, s, "blocked-replay", ForgetTargetManifest{
		Version: ForgetManifestVersionV2,
		Targets: []ForgetTarget{{
			EntryID: entry.Entry.ID, DomainID: DomainLegacyUnassigned,
			ExpectedRevision: entry.Entry.RowRevision,
		}},
		EntryIDs: []string{entry.Entry.ID},
	}, fmt.Sprintf(`{"actor":"owner@test","expected_revision":%d}`, entry.Entry.RowRevision), digest, keyID)
	if err := s.ledger.MarkBlocked(ctx, intent.Seq, "revision conflict"); err != nil {
		t.Fatal(err)
	}

	preview, err := s.PreviewForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Blocked != 1 || preview.Eligible != 0 || preview.EntriesRemovable != 0 {
		t.Fatalf("preview = %+v", preview)
	}
	result, err := s.ExecuteForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.CleanupJobs != 0 || result.Blocked != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := s.Get(ctx, entry.Entry.ID); err != nil {
		t.Fatal("blocked replay deleted the target")
	}
}

func TestForgetReplaySkipsUnapprovedPendingIntent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "unapproved replay target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := s.ledger.Digest(entry.Entry.Text)
	recordReplayIntent(t, s, "unapproved-replay", ForgetTargetManifest{
		Version: ForgetManifestVersionV2,
		Targets: []ForgetTarget{{
			EntryID: entry.Entry.ID, DomainID: DomainLegacyUnassigned,
			ExpectedRevision: entry.Entry.RowRevision,
		}},
		EntryIDs: []string{entry.Entry.ID},
	}, `{"kind":"entry"}`, digest, keyID)

	result, err := s.ExecuteForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.Unapproved != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := s.Get(ctx, entry.Entry.ID); err != nil {
		t.Fatal("unapproved replay deleted the target")
	}
}

func TestForgetReplayRejectsChangedRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "changed replay target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	approvedRevision := entry.Entry.RowRevision
	if _, err := s.Update(ctx, entry.Entry.ID, AddInput{Importance: 0.95}); err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := s.ledger.Digest(entry.Entry.Text)
	intent := recordReplayIntent(t, s, "changed-replay", ForgetTargetManifest{
		Version: ForgetManifestVersionV2,
		Targets: []ForgetTarget{{
			EntryID: entry.Entry.ID, DomainID: DomainLegacyUnassigned,
			ExpectedRevision: approvedRevision,
		}},
		EntryIDs: []string{entry.Entry.ID},
	}, fmt.Sprintf(`{"actor":"owner@test","expected_revision":%d}`, approvedRevision), digest, keyID)
	if err := s.ledger.MarkApplied(ctx, intent.Seq, 1); err != nil {
		t.Fatal(err)
	}

	result, err := s.ExecuteForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.Blocked != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := s.Get(ctx, entry.Entry.ID); err != nil {
		t.Fatal("stale replay deleted a changed target")
	}
}

func TestForgetReplayMultiTargetConflictIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.Add(ctx, AddInput{Text: "atomic replay first", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add(ctx, AddInput{Text: "atomic replay second", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	secondRevision := second.Entry.RowRevision
	if _, err := s.Update(ctx, second.Entry.ID, AddInput{Importance: 0.9}); err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := s.ledger.Digest(first.Entry.Text)
	recordReplayIntent(t, s, "atomic-replay", ForgetTargetManifest{
		Version: ForgetManifestVersionV2,
		Targets: []ForgetTarget{
			{EntryID: first.Entry.ID, DomainID: DomainLegacyUnassigned, ExpectedRevision: first.Entry.RowRevision},
			{EntryID: second.Entry.ID, DomainID: DomainLegacyUnassigned, ExpectedRevision: secondRevision},
		},
		EntryIDs: []string{first.Entry.ID, second.Entry.ID},
	}, `{"actor":"owner@test"}`, digest, keyID)

	result, err := s.ExecuteForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.Blocked != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := s.Get(ctx, first.Entry.ID); err != nil {
		t.Fatal("unchanged target was deleted despite sibling conflict")
	}
	if _, err := s.Get(ctx, second.Entry.ID); err != nil {
		t.Fatal("changed sibling target was deleted")
	}
}

func TestForgetReplayCreatesFreshCleanupProjection(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "replay projection target", Layer: LayerProfile,
		SessionKey: "projection-session", SourceType: SourceExplicit,
		EvidenceText: "replay projection target",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCleanupJobs(ctx, &fakeCleaner{}, 5); err != nil {
		t.Fatal(err)
	}
	done, err := s.ListJobs(ctx, JobDone, 10)
	if err != nil || len(done) != 1 {
		t.Fatalf("initial done jobs = %+v, err=%v", done, err)
	}
	if _, err := s.ExecuteForgetReplay(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := s.ListJobs(ctx, JobPending, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("replay pending jobs = %+v, err=%v", pending, err)
	}
	if pending[0].JobID == done[0].JobID {
		t.Fatal("replay reused a completed cleanup job")
	}
	if _, err := s.RunCleanupJobs(ctx, &fakeCleaner{}, 5); err != nil {
		t.Fatal(err)
	}
	done, err = s.ListJobs(ctx, JobDone, 10)
	if err != nil || len(done) != 2 {
		t.Fatalf("final done jobs = %+v, err=%v", done, err)
	}
}

func TestForgetReplayLegacyManifestRequiresDigestProof(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "legacy replay target", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := s.ledger.Digest(entry.Entry.Text)
	// No version, targets or revisions: only the selector HMAC can prove that
	// this restored row is the content that was forgotten.
	intent := recordReplayIntent(t, s, "legacy-replay", ForgetTargetManifest{
		EntryIDs: []string{entry.Entry.ID},
	}, `{"actor":"owner@test"}`, digest, keyID)
	if err := s.ledger.MarkApplied(ctx, intent.Seq, 1); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Eligible != 1 || preview.EntriesRemovable != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	result, err := s.ExecuteForgetReplay(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 {
		t.Fatalf("result = %+v", result)
	}
}
