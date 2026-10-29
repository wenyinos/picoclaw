package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/memstore"
)

// MemoryTool lets the model save and recall long-term memories.
type MemoryTool struct {
	workspace      string
	dbPath         string
	reviewRequired bool
}

// NewMemoryTool creates a memory tool bound to a workspace.
func NewMemoryTool(workspace, dbPath string, reviewRequired bool) *MemoryTool {
	return &MemoryTool{workspace: workspace, dbPath: dbPath, reviewRequired: reviewRequired}
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Description() string {
	return "Save or recall long-term memory. action=add (text, optional realm/layer/importance), " +
		"action=search (query, optional realm/layer/status), action=forget (id). Search covers all " +
		"memory layers and includes private memories. Use it for stable user facts, preferences, " +
		"relationship details and important events."
}

func (t *MemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "search", "forget"},
				"description": "Operation to perform",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Memory text for action=add",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query for action=search",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Memory id for action=forget",
			},
			"realm": map[string]any{
				"type":        "string",
				"enum":        []string{"real", "fictional", "work"},
				"description": "real=user facts, fictional=role-play, work=procedural",
			},
			"layer": map[string]any{
				"type":        "string",
				"enum":        []string{"profile", "relationship", "episodic", "procedural", "boundary"},
				"description": "Memory category",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"active", "pending_review", "superseded", "archived", "all"},
				"description": "Status filter for action=search; defaults to active",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "0..1 importance",
			},
		},
		"required": []string{"action"},
	}
}

func (t *MemoryTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	store, err := memstore.OpenForWorkspace(t.workspace, memstore.Config{
		Enabled: true, DBPath: t.dbPath, ReviewRequired: t.reviewRequired,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("memory store unavailable: %v", err))
	}

	switch action {
	case "add":
		text, _ := args["text"].(string)
		if strings.TrimSpace(text) == "" {
			return ErrorResult("text is required for action=add")
		}
		realm, _ := args["realm"].(string)
		if realm == "" {
			realm = memstore.RealmReal
		}
		layer, _ := args["layer"].(string)
		if layer == "" {
			layer = memstore.LayerProfile
		}
		importance, _ := args["importance"].(float64)
		res, err := store.Add(ctx, memstore.AddInput{
			Text: text, Realm: realm, Layer: layer, Importance: importance,
			Audience: "private", SourceType: "user_statement",
		})
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory add failed: %v", err))
		}
		if res.Entry != nil {
			return SilentResult(fmt.Sprintf("memory %s: %s (id: %s)", res.Action, res.Entry.Text, res.Entry.ID))
		}
		return SilentResult("memory " + res.Action)

	case "search":
		query, _ := args["query"].(string)
		realm, _ := args["realm"].(string)
		layer, _ := args["layer"].(string)
		status, _ := args["status"].(string)
		entries, err := store.SearchEntries(ctx, memstore.SearchOptions{
			Query: query, Realm: realm, Layer: layer, Status: status, Limit: 20,
		})
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory search failed: %v", err))
		}
		if len(entries) == 0 {
			return SilentResult("No matching memories.")
		}
		var b strings.Builder
		for _, e := range entries {
			b.WriteString("- [")
			b.WriteString(e.Realm)
			b.WriteString("/")
			b.WriteString(e.Layer)
			b.WriteString("] ")
			b.WriteString(e.Text)
			b.WriteString(" (id: ")
			b.WriteString(e.ID)
			b.WriteString(")\n")
		}
		return SilentResult(b.String())

	case "forget":
		id, _ := args["id"].(string)
		if strings.TrimSpace(id) == "" {
			return ErrorResult("id is required for action=forget")
		}
		if err := store.SetStatus(ctx, id, memstore.StatusArchived); err != nil {
			return ErrorResult(fmt.Sprintf("memory forget failed: %v", err))
		}
		return SilentResult("memory archived: " + id)

	default:
		return ErrorResult("unknown action: " + action)
	}
}
