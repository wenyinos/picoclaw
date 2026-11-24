package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
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
