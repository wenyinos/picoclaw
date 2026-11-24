package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memstore"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func TestMemoryToolGroupChatCannotWriteOrReadPrivate(t *testing.T) {
	workspace := t.TempDir()
	store, err := memstore.Open(filepath.Join(workspace, "memory", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	privateEntry, err := store.Add(context.Background(), memstore.AddInput{
		Text: "生日私密偏好", Layer: memstore.LayerProfile, Audience: "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(context.Background(), memstore.AddInput{
		Text: "生日公开信息", Layer: memstore.LayerProfile, Audience: "all",
	}); err != nil {
		t.Fatal(err)
	}

	groupCtx := toolshared.WithToolChatKind(
		toolshared.WithToolContext(context.Background(), "pico", "chat1"), "group")
	privateCtx := toolshared.WithToolChatKind(
		toolshared.WithToolContext(context.Background(), "pico", "chat1"), "private")
	tool := NewMemoryTool(workspace, "memory/memory.db", false).WithChannels(nil, false)

	if res := tool.Execute(groupCtx, map[string]any{
		"action": "add", "text": "群聊写入",
	}); !res.IsError {
		t.Fatal("group chat must not write personal memory")
	}
	if res := tool.Execute(groupCtx, map[string]any{
		"action": "update_state", "key": "stage", "value": "群聊阶段",
	}); !res.IsError {
		t.Fatal("group chat must not update relationship state")
	}
	if res := tool.Execute(privateCtx, map[string]any{
		"action": "add", "text": "我的密码是 abc123",
	}); !res.IsError {
		t.Fatal("secret must not be stored through the tool")
	}
	if res := tool.Execute(groupCtx, map[string]any{
		"action": "inspect", "id": privateEntry.Entry.ID,
	}); !res.IsError {
		t.Fatal("group chat must not inspect private memory")
	}
	if res := tool.Execute(groupCtx, map[string]any{
		"action": "confirm", "id": privateEntry.Entry.ID,
	}); !res.IsError {
		t.Fatal("group chat must not confirm private memory")
	}
	if res := tool.Execute(groupCtx, map[string]any{
		"action": "forget", "id": privateEntry.Entry.ID,
	}); !res.IsError {
		t.Fatal("group chat must not preview forget for private memory")
	}

	res := tool.Execute(groupCtx, map[string]any{"action": "search", "query": "生日"})
	if res.IsError {
		t.Fatalf("group search failed: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "生日私密偏好") {
		t.Fatalf("group search leaked private memory: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "生日公开信息") {
		t.Fatalf("group search missing shared memory: %s", res.ForLLM)
	}
}

func TestMemoryToolSearchAcrossLayers(t *testing.T) {
	workspace := t.TempDir()
	store, err := memstore.Open(filepath.Join(workspace, "memory", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := toolshared.WithToolContext(context.Background(), "pico", "")

	for _, in := range []memstore.AddInput{
		{Text: "我的生日是1989年6月4日", Realm: memstore.RealmReal, Layer: memstore.LayerProfile, Audience: "private"},
		{Text: "蓝色海豚 第七号仓库 搬箱子", Realm: memstore.RealmFictional, Layer: memstore.LayerProcedural, Audience: "all"},
		{Text: "token 的不同含义有哪些", Realm: memstore.RealmWork, Layer: memstore.LayerProcedural, Audience: "all"},
	} {
		if _, err := store.Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewMemoryTool(workspace, "memory/memory.db", false).WithChannels(nil, true)

	t.Run("profile and private memory", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"action": "search", "query": "生日"})
		if res.IsError {
			t.Fatalf("search error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "1989年6月4日") || !strings.Contains(res.ForLLM, "[real/profile]") {
			t.Fatalf("profile memory missing: %s", res.ForLLM)
		}
	})

	t.Run("realm filter", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{
			"action": "search", "query": "蓝色海豚", "realm": memstore.RealmFictional,
		})
		if res.IsError || !strings.Contains(res.ForLLM, "蓝色海豚") {
			t.Fatalf("fictional memory missing: %s", res.ForLLM)
		}

		res = tool.Execute(ctx, map[string]any{
			"action": "search", "query": "蓝色海豚", "realm": memstore.RealmWork,
		})
		if res.IsError || !strings.Contains(res.ForLLM, "No matching memories") {
			t.Fatalf("wrong realm should return no matches: %s", res.ForLLM)
		}
	})

	t.Run("garbage query", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{"action": "search", "query": "zzzqqqxxx不存在的乱码关键词"})
		if res.IsError || !strings.Contains(res.ForLLM, "No matching memories") {
			t.Fatalf("garbage query should return no matches: %s", res.ForLLM)
		}
	})

	t.Run("structured add and owner-only state", func(t *testing.T) {
		res := tool.Execute(ctx, map[string]any{
			"action": "add", "text": "用户希望被称为阿泽",
			"realm": "real", "layer": "profile",
			"subject": "user", "predicate": "nickname", "facet": "称呼", "value": "阿泽",
		})
		if res.IsError {
			t.Fatalf("structured add failed: %s", res.ForLLM)
		}
		res = tool.Execute(ctx, map[string]any{
			"action": "search", "query": "阿泽", "predicate": "nickname", "facet": "称呼",
		})
		if res.IsError || !strings.Contains(res.ForLLM, "阿泽") {
			t.Fatalf("structured search failed: %s", res.ForLLM)
		}

		res = tool.Execute(ctx, map[string]any{
			"action": "update_state", "key": "stage", "value": "测试阶段",
		})
		if !res.IsError {
			t.Fatal("update_state must require owner approval")
		}
		rel, err := store.GetRelationship(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rel {
			if r.Key == "stage" && r.Value == "测试阶段" {
				t.Fatalf("model changed relationship state: %+v", rel)
			}
		}
	})
}
