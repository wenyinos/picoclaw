package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memstore"
)

func TestContextBuilder_LongTermMemoryInjectionAndCacheInvalidation(t *testing.T) {
	workspace := t.TempDir()
	store, err := memstore.Open(filepath.Join(workspace, "memory", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	if _, err := store.UpsertPersona(ctx, "default", "Pico", "冷静、简洁，称呼用户为主人", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRelationship(ctx, "ai_name", "小P", 0.9); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(ctx, memstore.AddInput{
		Text: "用户喜欢喝乌龙茶", Layer: memstore.LayerProfile, Importance: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	cb := NewContextBuilder(workspace).WithLongTermMemory(store, config.MemoryConfig{
		Enabled: true, BudgetTokens: 1200, PrivateChannels: []string{"pico"},
	})
	build := func(msg string) string {
		msgs := cb.BuildMessages(nil, "", msg, nil, "pico", "chat1", "user1", "User")
		if len(msgs) == 0 {
			t.Fatal("no messages built")
		}
		return msgs[0].Content
	}

	system := build("今天喝什么茶好")
	for _, want := range []string{"小P", "乌龙茶", "角色设定"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, system)
		}
	}
	for _, forbidden := range []string{"MEMORY.md", "Daily Notes"} {
		if strings.Contains(system, forbidden) {
			t.Fatalf("system prompt still references legacy memory file %q:\n%s", forbidden, system)
		}
	}
	if !strings.Contains(system, "`memory` tool") {
		t.Fatalf("system prompt missing memory tool instruction:\n%s", system)
	}

	// A memory write must invalidate the cached system prompt.
	if _, err := store.Add(ctx, memstore.AddInput{
		Text: "不要在工作群提到亲密称呼", Layer: memstore.LayerBoundary, Importance: 0.95, Audience: "private",
	}); err != nil {
		t.Fatal(err)
	}
	system = build("今天喝什么茶好")
	if !strings.Contains(system, "不要在工作群") {
		t.Fatalf("cached prompt did not pick up new boundary:\n%s", system)
	}

	// Query-relevant episodic memory appears only for its private channel.
	if _, err := store.Add(ctx, memstore.AddInput{
		Text: "2026年9月一起去看了海", Layer: memstore.LayerEpisodic, Realm: memstore.RealmReal,
		Importance: 0.8, EmotionalWeight: 0.9, Audience: "private",
	}); err != nil {
		t.Fatal(err)
	}
	system = build("还记得我们去看海吗")
	if !strings.Contains(system, "一起去看了海") {
		t.Fatalf("retrieval missing for private channel:\n%s", system)
	}
}

func TestContextBuilder_DoesNotInjectLegacyMemoryFiles(t *testing.T) {
	workspace := t.TempDir()
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(filepath.Join(memoryDir, "202610"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "MEMORY.md"), []byte("# Memory\nLEGACY_MD_MARKER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memoryDir, "202610", "20261025.md"), []byte("LEGACY_DAILY_MARKER"), 0o644); err != nil {
		t.Fatal(err)
	}

	cb := NewContextBuilder(workspace)
	msgs := cb.BuildMessages(nil, "", "hello", nil, "pico", "chat1", "user1", "User")
	if len(msgs) == 0 {
		t.Fatal("no messages built")
	}
	system := msgs[0].Content
	for _, marker := range []string{"LEGACY_MD_MARKER", "LEGACY_DAILY_MARKER", "MEMORY.md", "Daily Notes"} {
		if strings.Contains(system, marker) {
			t.Fatalf("system prompt still reads legacy memory marker %q:\n%s", marker, system)
		}
	}
}
