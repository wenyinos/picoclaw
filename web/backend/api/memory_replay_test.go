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
