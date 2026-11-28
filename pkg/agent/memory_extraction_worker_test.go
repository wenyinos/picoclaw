package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestRunExtractionJobsProcessesDurableReference(t *testing.T) {
	workspace := t.TempDir()
	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Memory.Enabled = true
	cfg.Memory.AutoExtract = true
	cfg.Memory.ReviewRequired = false
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("default agent missing")
	}
	const sessionKey = "extract-worker-session"
	const text = "以后叫我小舟"
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{Role: "user", Content: text})
	if err := agent.Sessions.Save(sessionKey); err != nil {
		t.Fatal(err)
	}
	store, err := memstore.OpenForWorkspace(workspace, memstore.Config{
		Enabled: true, DBPath: "memory/memory.db",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := memstore.ExtractSource{
		EventID:    memstore.BuildOriginEventID("pico", sessionKey, "msg-1", "user"),
		SessionKey: sessionKey, MessageID: "msg-1", Role: "user",
		DomainID: memstore.DomainLegacyUnassigned,
	}
	if _, err := store.EnqueueExtractionJob(context.Background(), source, text); err != nil {
		t.Fatal(err)
	}
	al.runExtractionJobs(context.Background())
	jobs, err := store.ListJobs(context.Background(), memstore.JobDone, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("done extraction jobs = %+v", jobs)
	}
	entries, err := store.List(context.Background(), memstore.SearchOptions{
		Query: "小舟", Limit: 10, DomainID: memstore.DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("no extracted memory in %s", filepath.Join(workspace, "memory", "memory.db"))
	}
}
