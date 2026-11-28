package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memstore"
)

func TestMemoryAPI_ReplayDefaultsToPreviewAndSkipsBlocked(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	store, _, err := h.memoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	entry, err := store.Add(ctx, memstore.AddInput{
		Text: "api blocked replay target", Layer: memstore.LayerProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, keyID, _ := store.ForgetLedgerRef().Digest(entry.Entry.Text)
	intent, err := store.ForgetLedgerRef().RecordIntent(ctx, memstore.ForgetIntent{
		RequestID: "api-blocked-replay", DomainID: memstore.DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest, KeyID: keyID,
		ScopeJSON: `{"actor":"owner@test"}`,
		TargetManifest: memstore.MarshalManifest(memstore.ForgetTargetManifest{
			Version: memstore.ForgetManifestVersionV2,
			Targets: []memstore.ForgetTarget{{
				EntryID: entry.Entry.ID, DomainID: memstore.DomainLegacyUnassigned,
				ExpectedRevision: entry.Entry.RowRevision,
			}},
			EntryIDs: []string{entry.Entry.ID},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetLedgerRef().MarkBlocked(ctx, intent.Seq, "revision conflict"); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)
	rec := doJSON(t, mux, http.MethodPost, "/api/memory/ledger/replay", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var preview memstore.ForgetReplayPreview
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Blocked != 1 || preview.Eligible != 0 {
		t.Fatalf("preview = %+v", preview)
	}

	rec = doJSON(t, mux, http.MethodPost, "/api/memory/ledger/replay",
		map[string]any{"confirm": true})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("execute status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var result memstore.ForgetReplayResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Removed != 0 || result.Blocked != 1 {
		t.Fatalf("result = %+v", result)
	}
	reopened, _, err := h.memoryStore()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Get(ctx, entry.Entry.ID); err != nil {
		t.Fatal("blocked API replay deleted the target")
	}
}

func TestMemoryAPI_ImportIsCLIOnly(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)
	rec := doJSON(t, mux, http.MethodPost, "/api/memory/import",
		map[string]any{"workspace": "/tmp/arbitrary"})
	if rec.Code != http.StatusGone {
		t.Fatalf("import status = %d, want 410; body=%s", rec.Code, rec.Body.String())
	}
}

func TestMemoryAPI_OriginRetractionRequiresConfirmation(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	store, _, err := h.memoryStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	eventID := memstore.BuildOriginEventID("pico", "api-origin", "msg-1", "user")
	if err := store.RecordOriginEvent(ctx, memstore.OriginEvent{
		EventID: eventID, SourceBackend: "jsonl", SessionKey: "api-origin",
		MessageID: "msg-1", Role: "user", Available: true,
		DomainID: memstore.DomainLegacyUnassigned,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Add(ctx, memstore.AddInput{
		Text: "api origin retraction target", Layer: memstore.LayerProfile,
		SessionKey: "api-origin", SourceType: memstore.SourceExplicit,
		EvidenceText: "api origin retraction target", OriginEventID: eventID,
		OriginRevision: 1, DomainID: memstore.DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)
	path := "/api/memory/origins/" + eventID + "/unavailable"
	rec := doJSON(t, mux, http.MethodPost, path, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed retraction status = %d", rec.Code)
	}
	rec = doJSON(t, mux, http.MethodPost, path, map[string]any{"confirm": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("retraction status = %d, body=%s", rec.Code, rec.Body.String())
	}
	reloaded, err := store.Get(ctx, entry.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.EvidenceStatus != "unsupported" {
		t.Fatalf("evidence status = %q, want unsupported", reloaded.EvidenceStatus)
	}
}

func TestMemoryAPI_RecoveryAndRequeueBlockedCleanup(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	store, _, err := h.memoryStore()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, err := store.Add(ctx, memstore.AddInput{
		Text: "api requeue target without a locator", Layer: memstore.LayerProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Forget(ctx, entry.Entry.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RunCleanupJobs(ctx, &apiFakeCleaner{}, 5); err != nil {
		t.Fatal(err)
	}
	// OpenForWorkspace caches stores for the process; do not close it while the
	// API handler still resolves the same path.

	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)
	rec := doJSON(t, mux, http.MethodGet, "/api/memory/recovery", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var recovery memstore.RecoveryStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &recovery); err != nil {
		t.Fatal(err)
	}
	if recovery.Ready || recovery.BlockedCleanup != 1 {
		t.Fatalf("recovery = %+v", recovery)
	}

	rec = doJSON(t, mux, http.MethodGet, "/api/memory/jobs?state=blocked", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("jobs status = %d", rec.Code)
	}
	var jobs struct {
		Jobs []memstore.Job `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Jobs) != 1 {
		t.Fatalf("blocked jobs = %+v", jobs.Jobs)
	}
	jobID := jobs.Jobs[0].JobID

	rec = doJSON(t, mux, http.MethodPost, "/api/memory/jobs/"+jobID+"/requeue", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed requeue status = %d, want 400", rec.Code)
	}
	rec = doJSON(t, mux, http.MethodPost, "/api/memory/jobs/"+jobID+"/requeue",
		map[string]any{"confirm": true})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("requeue status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var successor memstore.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &successor); err != nil {
		t.Fatal(err)
	}
	if successor.State != memstore.JobPending || successor.JobID == jobID {
		t.Fatalf("successor = %+v", successor)
	}
}

type apiFakeCleaner struct{}

func (c *apiFakeCleaner) CleanLocators(_ context.Context, locators []memstore.SourceLocator) (memstore.CleanupOutcome, error) {
	return memstore.CleanupOutcome{}, nil
}
