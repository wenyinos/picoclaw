package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/memstore"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// MemoryTool lets the model save and recall long-term memories.
type MemoryTool struct {
	workspace         string
	dbPath            string
	reviewRequired    bool
	privateExclusions []string
	injectFictional   bool
}

// NewMemoryTool creates a memory tool bound to a workspace.
func NewMemoryTool(workspace, dbPath string, reviewRequired bool) *MemoryTool {
	return &MemoryTool{workspace: workspace, dbPath: dbPath, reviewRequired: reviewRequired}
}

// WithChannels sets the per-channel exclusion policy: every channel is private
// by default and the listed ones are opted out (0.7.3 semantics).
func (t *MemoryTool) WithChannels(privateExclusions []string, injectFictional bool) *MemoryTool {
	t.privateExclusions = append([]string(nil), privateExclusions...)
	t.injectFictional = injectFictional
	return t
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Description() string {
	return "Manage long-term memory. action=add (text, optional realm/layer/subject/predicate/facet/value/importance), " +
		"action=search (query, optional realm/layer/predicate/facet/status), action=inspect (id), " +
		"action=correct (id + text, non-core only), action=forget (id, preview only), " +
		"action=load_capsule (preset=t0|t0_compact or cluster_id). " +
		"Confirmation, archive/restore and relationship state updates require owner approval in the dashboard or CLI."
}

func (t *MemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"add", "search", "inspect", "correct", "forget", "load_capsule",
				},
				"description": "Operation to perform",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Memory text for action=add or corrected text for action=correct",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query for action=search",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "Memory id for inspect/confirm/correct/archive/restore/forget",
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
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "For action=forget: only report what would be removed",
			},
			"cluster_id": map[string]any{
				"type":        "string",
				"description": "Scene capsule id for action=load_capsule",
			},
			"preset": map[string]any{
				"type":        "string",
				"enum":        []string{"t0", "t0_compact"},
				"description": "Capsule preset: t0=full, t0_compact=compact",
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
	// Every model tool call goes through the governed service with the trusted
	// request context. The model cannot self-approve, widen the domain or write
	// personal memory from a group chat.
	mc := memstore.MemoryContext{
		DomainID:   memstore.DomainLegacyUnassigned,
		ChatKind:   toolshared.ToolChatKind(ctx),
		Channel:    toolshared.ToolChannel(ctx),
		SessionKey: toolshared.ToolSessionKey(ctx),
		Purpose:    "chat",
	}
	svc := memstore.NewMemoryService(store, memstore.Config{
		Enabled:                  true,
		DBPath:                   t.dbPath,
		ReviewRequired:           t.reviewRequired,
		PrivateChannelExclusions: append([]string(nil), t.privateExclusions...),
	})
	recordOrigin := func(action, text string) (string, error) {
		nativeID := toolshared.ToolMessageID(ctx)
		originID := memstore.BuildOriginEventID(
			mc.Channel, mc.SessionKey, nativeID, "memory_tool", action, text)
		if err := svc.RecordOriginEvent(ctx, mc, memstore.OriginEvent{
			EventID: originID, OriginRevision: 1, SourceBackend: "jsonl",
			SourceLocator: mc.SessionKey, SessionKey: mc.SessionKey,
			MessageID: nativeID, Role: "assistant", EventAt: time.Now().UnixMilli(),
			Available: true, DomainID: mc.DomainID,
		}); err != nil {
			return "", err
		}
		return originID, nil
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
		originID, err := recordOrigin("add", text)
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory source unavailable: %v", err))
		}
		res, err := svc.Add(ctx, mc, memstore.AddInput{
			Text: text, Realm: realm, Layer: layer, Importance: importance,
			Subject: subject, Predicate: predicate, Facet: facet, Value: value,
			SupersedesID: supersedesID, EmotionalWeight: emotionalWeight, Valence: valence,
			Audience: "private", SourceType: memstore.SourceInferred, EvidenceText: text,
			SessionKey: mc.SessionKey, EvidenceRootType: "jsonl.tool_text",
			OriginEventID: originID, OriginRevision: 1,
			// Model-driven writes stay in the isolated legacy partition and
			// the classifier can only raise protection, never lower it.
			DomainID:    memstore.DomainLegacyUnassigned,
			Sensitivity: memstore.SensitivityForText(text),
		})
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory add failed: %v", err))
		}
		if res.Action == "blocked_forget" {
			return SilentResult("memory blocked: this content was explicitly forgotten")
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
		// The real channel comes from the request context, never from a model
		// parameter, so a public conversation cannot read private memory by
		// asking for it (B01).
		channel := toolshared.ToolChannel(ctx)
		if strings.TrimSpace(channel) == "" {
			return ErrorResult("memory search needs a trusted channel context")
		}
		entries, err := svc.Search(ctx, mc, memstore.SearchOptions{
			Query: query, Realm: realm, Layer: layer, Predicate: predicate,
			Facet: facet, Status: status, Limit: 20,
			AllLayers: true, IncludeFic: t.injectFictional,
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

	case "inspect":
		id, _ := args["id"].(string)
		if strings.TrimSpace(id) == "" {
			return ErrorResult("id is required for action=inspect")
		}
		if strings.TrimSpace(toolshared.ToolChannel(ctx)) == "" {
			return ErrorResult("memory inspect needs a trusted channel context")
		}
		if _, err := svc.Get(ctx, mc, id); err != nil {
			// Same message for "missing" and "not visible": no enumeration.
			return ErrorResult("memory not found")
		}
		res, err := store.Inspect(ctx, id)
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory inspect failed: %v", err))
		}
		return SilentResult(formatInspect(res))

	case "confirm":
		return ErrorResult("memory confirm requires owner approval in the dashboard or CLI")

	case "correct":
		id, _ := args["id"].(string)
		text, _ := args["text"].(string)
		if strings.TrimSpace(id) == "" || strings.TrimSpace(text) == "" {
			return ErrorResult("id and text are required for action=correct")
		}
		realm, _ := args["realm"].(string)
		layer, _ := args["layer"].(string)
		predicate, _ := args["predicate"].(string)
		facet, _ := args["facet"].(string)
		value, _ := args["value"].(string)
		originID, err := recordOrigin("correct", text)
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory source unavailable: %v", err))
		}
		res, err := svc.Correct(ctx, mc, id, memstore.AddInput{
			Text: text, Realm: realm, Layer: layer, Predicate: predicate, Facet: facet,
			Value: value, SourceType: memstore.SourceInferred, EvidenceText: text,
			SessionKey: mc.SessionKey, EvidenceRootType: "jsonl.tool_text",
			OriginEventID: originID, OriginRevision: 1,
		})
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory correct failed: %v", err))
		}
		if res.Action == "blocked_forget" {
			return SilentResult("memory blocked: this content was explicitly forgotten")
		}
		if res.Entry == nil {
			return SilentResult("memory corrected: " + id)
		}
		return SilentResult(fmt.Sprintf("memory corrected: %s (new id: %s)", id, res.Entry.ID))

	case "archive", "restore":
		return ErrorResult("memory archive/restore requires owner approval in the dashboard or CLI")

	case "forget":
		id, _ := args["id"].(string)
		if strings.TrimSpace(id) == "" {
			return ErrorResult("id is required for action=forget")
		}
		// Permanent forget is an owner decision. The model may only preview
		// the impact; a model-requested erase must be approved by the host
		// through the dashboard or CLI (FR-05).
		preview, err := svc.Forget(ctx, mc, id, true)
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory forget preview failed: %v", err))
		}
		if preview.AlreadyGone {
			return SilentResult("memory already gone: " + id)
		}
		return SilentResult(fmt.Sprintf(
			"forget preview only: %s would remove %d source(s), %d relation(s), %d summary(ies), relationship keys=%v. "+
				"Permanent erase needs owner approval in the memory dashboard.",
			id, preview.Sources, preview.Relations, preview.Summaries, preview.RelationshipKeys))

	case "load_capsule":
		clusterID, _ := args["cluster_id"].(string)
		preset, _ := args["preset"].(string)
		switch strings.ToLower(strings.TrimSpace(preset)) {
		case "t0":
			clusterID = "capsule:fictional_t0"
		case "t0_compact":
			clusterID = "capsule:fictional_t0_compact"
		}
		if strings.TrimSpace(clusterID) == "" {
			return ErrorResult("cluster_id or preset is required for action=load_capsule")
		}
		// Capsules are scoped to the actual request channel; selecting the
		// first configured private channel would leak into public chats (B01).
		channel := toolshared.ToolChannel(ctx)
		if strings.TrimSpace(channel) == "" {
			return ErrorResult("memory load_capsule needs a trusted channel context")
		}
		capsule, err := store.LoadCapsuleFor(ctx, clusterID, channel,
			toolshared.ToolChatKind(ctx), t.privateExclusions, t.injectFictional, 1200)
		if err != nil {
			return ErrorResult(fmt.Sprintf("memory load_capsule failed: %v", err))
		}
		if len(capsule.Items) == 0 {
			return SilentResult("capsule is empty for this channel")
		}
		return SilentResult(capsule.CapsuleText())

	case "update_state":
		return ErrorResult("memory update_state requires owner approval in the dashboard or CLI")

	default:
		return ErrorResult("unknown action: " + action)
	}
}

func formatInspect(res *memstore.InspectResult) string {
	if res == nil {
		return "memory not found"
	}
	e := res.Entry
	var b strings.Builder
	fmt.Fprintf(&b, "memory %s\n", e.ID)
	fmt.Fprintf(&b, "realm/layer: %s/%s  status: %s  pinned: %v\n", e.Realm, e.Layer, e.Status, e.PinnedCore)
	fmt.Fprintf(&b, "subject/predicate/facet: %s / %s / %s\n", e.Subject, e.Predicate, e.Facet)
	if e.EventAt != nil {
		fmt.Fprintf(&b, "event_at: %d  precision: %s  source: %s\n",
			*e.EventAt, e.EventTimePrecision, e.EventTimeSource)
	}
	if e.ValidFrom != nil || e.ValidTo != nil {
		fmt.Fprintf(&b, "valid: %v .. %v\n", derefInt(e.ValidFrom), derefInt(e.ValidTo))
	}
	fmt.Fprintf(&b, "text: %s\n", e.Text)
	fmt.Fprintf(&b, "sources: %d\n", len(res.Sources))
	for _, s := range res.Sources {
		fmt.Fprintf(&b, "  - [%s] confidence=%.2f confirmed=%v %s\n",
			s.SourceType, s.Confidence, s.UserConfirmed, s.EvidenceText)
	}
	if len(res.Supersedes) > 0 {
		fmt.Fprintf(&b, "supersede edges: %d\n", len(res.Supersedes))
		for _, r := range res.Supersedes {
			fmt.Fprintf(&b, "  - %s -> %s\n", r.SourceID, r.TargetID)
		}
	}
	if len(res.Relations) > 0 {
		fmt.Fprintf(&b, "relations: %d\n", len(res.Relations))
		for _, r := range res.Relations {
			fmt.Fprintf(&b, "  - %s %s %s\n", r.SourceID, r.RelationType, r.TargetID)
		}
	}
	if len(res.Operations) > 0 {
		fmt.Fprintf(&b, "operations: %d\n", len(res.Operations))
		for _, op := range res.Operations {
			fmt.Fprintf(&b, "  - %s at %d reason=%s\n", op.Operation, op.CreatedAt, op.Reason)
		}
	}
	if len(res.Clusters) > 0 {
		fmt.Fprintf(&b, "clusters: %s\n", strings.Join(res.Clusters, ", "))
	}
	return b.String()
}

func derefInt(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
