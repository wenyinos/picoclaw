package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/memstore"
)

// MemoryTool lets the model save and recall long-term memories.
type MemoryTool struct {
	workspace      string
	dbPath         string
	reviewRequired bool
}

var allowedRelationshipKeys = map[string]bool{
	"ai_name": true, "user_name": true, "stage": true, "relationship": true,
	"intimacy": true, "current_mood": true, "tension_mode": true, "rituals": true,
	"anniversary": true, "unresolved": true, "boundary": true,
}

// NewMemoryTool creates a memory tool bound to a workspace.
func NewMemoryTool(workspace, dbPath string, reviewRequired bool) *MemoryTool {
	return &MemoryTool{workspace: workspace, dbPath: dbPath, reviewRequired: reviewRequired}
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Description() string {
	return "Save or recall long-term memory. action=add (text, optional realm/layer/subject/predicate/facet/value/importance), " +
		"action=search (query, optional realm/layer/predicate/facet/status), action=update_state (key/value/ttl_seconds), " +
		"action=forget (id). Search covers all memory layers and includes private memories."
}

func (t *MemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "search", "update_state", "forget"},
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
			"subject": map[string]any{
				"type":        "string",
				"description": "Structured subject, e.g. user / ai / 姐弟故事",
			},
			"predicate": map[string]any{
				"type":        "string",
				"description": "Structured predicate from the controlled vocabulary",
			},
			"facet": map[string]any{
				"type":        "string",
				"description": "Optional facet within a predicate, e.g. 称呼 / 家庭压力",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Short scalar value for structured facts, or value for update_state",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Relationship state key for action=update_state",
			},
			"supersedes_id": map[string]any{
				"type":        "string",
				"description": "Explicitly supersede this active memory id",
			},
			"emotional_weight": map[string]any{
				"type":        "number",
				"description": "0..1 emotional weight",
			},
			"valence": map[string]any{
				"type":        "number",
				"description": "-1..1 emotional valence",
			},
			"ttl_seconds": map[string]any{
				"type":        "number",
				"description": "Optional TTL for action=update_state",
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
		subject, _ := args["subject"].(string)
		predicate, _ := args["predicate"].(string)
		facet, _ := args["facet"].(string)
		value, _ := args["value"].(string)
		supersedesID, _ := args["supersedes_id"].(string)
		emotionalWeight, _ := args["emotional_weight"].(float64)
		valence, _ := args["valence"].(float64)
		res, err := store.Add(ctx, memstore.AddInput{
			Text: text, Realm: realm, Layer: layer, Importance: importance,
			Subject: subject, Predicate: predicate, Facet: facet, Value: value,
			SupersedesID: supersedesID, EmotionalWeight: emotionalWeight, Valence: valence,
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
		predicate, _ := args["predicate"].(string)
		facet, _ := args["facet"].(string)
		status, _ := args["status"].(string)
		entries, err := store.SearchEntries(ctx, memstore.SearchOptions{
			Query: query, Realm: realm, Layer: layer, Predicate: predicate,
			Facet: facet, Status: status, Limit: 20,
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

	case "update_state":
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		key = strings.TrimSpace(key)
		if key == "" || strings.TrimSpace(value) == "" {
			return ErrorResult("key and value are required for action=update_state")
		}
		if !allowedRelationshipKeys[key] {
			return ErrorResult("unsupported relationship state key: " + key)
		}
		var expires *int64
		if ttl, ok := args["ttl_seconds"].(float64); ok && ttl > 0 {
			v := time.Now().Add(time.Duration(ttl * float64(time.Second))).UnixMilli()
			expires = &v
		}
		if err := store.SetRelationshipWithTTL(ctx, key, value, 0.9, expires); err != nil {
			return ErrorResult(fmt.Sprintf("memory update_state failed: %v", err))
		}
		return SilentResult("relationship state updated: " + key)

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
