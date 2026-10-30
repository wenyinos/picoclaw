package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memstore"
)

func TestMemoryToolSearchAcrossLayers(t *testing.T) {
	workspace := t.TempDir()
	store, err := memstore.Open(filepath.Join(workspace, "memory", "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	for _, in := range []memstore.AddInput{
		{Text: "我的生日是1989年6月4日", Realm: memstore.RealmReal, Layer: memstore.LayerProfile, Audience: "private"},
		{Text: "蓝色海豚 第七号仓库 搬箱子", Realm: memstore.RealmFictional, Layer: memstore.LayerProcedural, Audience: "all"},
		{Text: "token 的不同含义有哪些", Realm: memstore.RealmWork, Layer: memstore.LayerProcedural, Audience: "all"},
	} {
		if _, err := store.Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewMemoryTool(workspace, "memory/memory.db", false)

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

	t.Run("structured add and state", func(t *testing.T) {
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
		if res.IsError {
			t.Fatalf("update_state failed: %s", res.ForLLM)
		}
		rel, err := store.GetRelationship(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range rel {
			if r.Key == "stage" && r.Value == "测试阶段" {
				found = true
			}
		}
		if !found {
			t.Fatalf("stage not updated: %+v", rel)
		}
	})
}
