package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memstore"
)

func TestRunForgetCleanupRunsWhenMemoryDisabled(t *testing.T) {
	workspace := t.TempDir()
	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Memory.Enabled = false

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	al.runForgetCleanup(ctx)

	dbPath := filepath.Join(workspace, "memory", "memory.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("cleanup did not open the forget store with memory disabled: %v", err)
	}
}

func TestRunForgetCleanupReconcilesApprovedPendingIntent(t *testing.T) {
	workspace := t.TempDir()
	dbPath := filepath.Join(workspace, "memory", "memory.db")
	store, err := memstore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, err := store.Add(ctx, memstore.AddInput{
		Text: "approved pending worker target", Layer: memstore.LayerProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger := store.ForgetLedgerRef()
	digest, keyID, version := ledger.Digest(entry.Entry.Text)
	if _, err := ledger.RecordIntent(ctx, memstore.ForgetIntent{
		RequestID: "worker-approved", DomainID: memstore.DomainLegacyUnassigned,
		SelectorType: "entry+content_hmac", SelectorDigest: digest,
		HashVersion: version, KeyID: keyID,
		ScopeJSON: fmt.Sprintf(`{"actor":"dashboard@test","expected_revision":%d}`,
			entry.Entry.RowRevision),
		TargetManifest: memstore.MarshalManifest(memstore.ForgetTargetManifest{
			EntryIDs: []string{entry.Entry.ID},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Memory.Enabled = false
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go al.runForgetCleanup(runCtx)

	deadline := time.Now().Add(2 * time.Second)
	for {
		check, err := memstore.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		_, getErr := check.Get(ctx, entry.Entry.ID)
		_ = check.Close()
		if getErr != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background worker did not reconcile the approved pending intent")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
