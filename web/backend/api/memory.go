package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memstore"
)

func (h *Handler) registerMemoryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/memory/overview", h.handleMemoryOverview)
	mux.HandleFunc("GET /api/memory/entries", h.handleMemoryEntries)
	mux.HandleFunc("POST /api/memory/entries", h.handleMemoryAddEntry)
	mux.HandleFunc("GET /api/memory/entries/{id}", h.handleMemoryGetEntry)
	mux.HandleFunc("GET /api/memory/entries/{id}/inspect", h.handleMemoryInspectEntry)
	mux.HandleFunc("PUT /api/memory/entries/{id}", h.handleMemoryUpdateEntry)
	mux.HandleFunc("POST /api/memory/entries/{id}/correct", h.handleMemoryCorrectEntry)
	mux.HandleFunc("POST /api/memory/entries/{id}/forget", h.handleMemoryForgetEntry)
	mux.HandleFunc("POST /api/memory/entries/{id}/archive", h.handleMemoryArchiveEntry)
	mux.HandleFunc("POST /api/memory/entries/{id}/restore", h.handleMemoryRestoreEntry)
	mux.HandleFunc("GET /api/memory/domains", h.handleMemoryDomains)
	mux.HandleFunc("GET /api/memory/ledger/health", h.handleMemoryLedgerHealth)
	mux.HandleFunc("POST /api/memory/ledger/replay", h.handleMemoryLedgerReplay)
	mux.HandleFunc("GET /api/memory/clusters", h.handleMemoryClusters)
	mux.HandleFunc("POST /api/memory/clusters", h.handleMemoryCreateCluster)
	mux.HandleFunc("POST /api/memory/clusters/{id}/members", h.handleMemoryClusterMembers)
	mux.HandleFunc("POST /api/memory/clusters/{id}/summaries", h.handleMemoryGenerateSummary)
	mux.HandleFunc("GET /api/memory/clusters/{id}/summaries", h.handleMemoryListSummaries)
	mux.HandleFunc("GET /api/memory/summaries/{id}/evidence", h.handleMemorySummaryEvidence)
	mux.HandleFunc("GET /api/memory/commitments", h.handleMemoryCommitments)
	mux.HandleFunc("POST /api/memory/commitments", h.handleMemoryCreateCommitment)
	mux.HandleFunc("POST /api/memory/commitments/{id}/status", h.handleMemoryCommitmentStatus)
	mux.HandleFunc("GET /api/memory/recalls", h.handleMemoryRecalls)
	mux.HandleFunc("POST /api/memory/recalls/{id}/feedback", h.handleMemoryRecallFeedback)
	mux.HandleFunc("GET /api/memory/jobs", h.handleMemoryJobs)
	mux.HandleFunc("GET /api/memory/plans", h.handleMemoryPlans)
	mux.HandleFunc("POST /api/memory/plans/{id}/approve", h.handleMemoryApprovePlan)
	mux.HandleFunc("GET /api/memory/review", h.handleMemoryReview)
	mux.HandleFunc("POST /api/memory/review/{id}/resolve", h.handleMemoryResolveReview)
	mux.HandleFunc("GET /api/memory/relationship", h.handleMemoryRelationship)
	mux.HandleFunc("PUT /api/memory/relationship", h.handleMemoryUpdateRelationship)
	mux.HandleFunc("GET /api/memory/persona", h.handleMemoryPersona)
	mux.HandleFunc("PUT /api/memory/persona", h.handleMemoryUpdatePersona)
	mux.HandleFunc("GET /api/memory/settings", h.handleMemorySettings)
	mux.HandleFunc("PUT /api/memory/settings", h.handleMemoryUpdateSettings)
	mux.HandleFunc("POST /api/memory/compact", h.handleMemoryCompact)
	mux.HandleFunc("POST /api/memory/repair/preview", h.handleMemoryRepairPreview)
	mux.HandleFunc("POST /api/memory/repair/apply", h.handleMemoryRepairApply)
	mux.HandleFunc("GET /api/memory/migration/status", h.handleMemoryMigrationStatus)
	mux.HandleFunc("POST /api/memory/migration/apply", h.handleMemoryMigrationApply)
	mux.HandleFunc("GET /api/memory/ops", h.handleMemoryOps)
	mux.HandleFunc("GET /api/memory/capsule/{id}", h.handleMemoryCapsule)
	mux.HandleFunc("POST /api/memory/import", h.handleMemoryImport)
	mux.HandleFunc("GET /api/memory/export", h.handleMemoryExport)
	mux.HandleFunc("GET /api/memory/stats", h.handleMemoryStats)
}

func (h *Handler) memoryStore() (*memstore.Store, *config.Config, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, nil, err
	}
	store, err := memstore.OpenForWorkspace(cfg.Agents.Defaults.Workspace, memstore.Config{
		Enabled:                  cfg.Memory.Enabled,
		DBPath:                   cfg.Memory.DBPath,
		BudgetTokens:             cfg.Memory.BudgetTokens,
		AutoExtract:              cfg.Memory.AutoExtract,
		PrivateChannelExclusions: cfg.Memory.PrivateChannelExclusions,
		InjectFictional:          cfg.Memory.InjectFictional,
		ReviewRequired:           cfg.Memory.ReviewRequired,
		EpisodicRetentionDays:    cfg.Memory.EpisodicRetentionDays,
	})
	if err != nil {
		return nil, nil, err
	}
	return store, cfg, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handler) handleMemoryOverview(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o, err := store.Overview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (h *Handler) handleMemoryEntries(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	entries, err := store.List(r.Context(), memstore.SearchOptions{
		Query: q.Get("q"), Realm: q.Get("realm"), Layer: q.Get("layer"),
		Status: q.Get("status"), Audience: q.Get("audience"), Scope: q.Get("scope"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) handleMemoryGetEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	e, err := store.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func decodeBody[T any](r *http.Request) (T, error) {
	var v T
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return v, err
	}
	defer r.Body.Close()
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return v, fmt.Errorf("unexpected trailing JSON value")
	}
	return v, nil
}

// memoryEntryRequest is the owner-facing write DTO. Governance fields such as
// Force, BypassForgetTombstone, domain, confirmations and origin identities are
// deliberately absent: only the host may set them.
type memoryEntryRequest struct {
	Text            string         `json:"text"`
	Realm           string         `json:"realm"`
	Layer           string         `json:"layer"`
	Scope           string         `json:"scope"`
	Audience        string         `json:"audience"`
	Subject         string         `json:"subject"`
	Predicate       string         `json:"predicate"`
	Facet           string         `json:"facet"`
	Value           string         `json:"value"`
	Importance      float64        `json:"importance"`
	EmotionalWeight float64        `json:"emotional_weight"`
	Valence         float64        `json:"valence"`
	Confidence      float64        `json:"confidence"`
	ExpiresAt       *int64         `json:"expires_at"`
	EventAt         *int64         `json:"event_at"`
	EventStart      *int64         `json:"event_start"`
	EventEnd        *int64         `json:"event_end"`
	TemplateKey     string         `json:"template_key"`
	SupersedesID    string         `json:"supersedes_id"`
	EvidenceText    string         `json:"evidence_text"`
	EventPrecision  string         `json:"event_time_precision"`
	EventTimezone   string         `json:"event_timezone"`
	EventTimeSource string         `json:"event_time_source"`
	ValidFrom       *int64         `json:"valid_from"`
	ValidTo         *int64         `json:"valid_to"`
	Qualifiers      string         `json:"qualifiers"`
	Attributes      map[string]any `json:"attributes"`
}

func (dto memoryEntryRequest) addInput() memstore.AddInput {
	return memstore.AddInput{
		Text: dto.Text, Realm: dto.Realm, Layer: dto.Layer, Scope: dto.Scope,
		Audience: dto.Audience, Subject: dto.Subject, Predicate: dto.Predicate,
		Facet: dto.Facet, Value: dto.Value, Importance: dto.Importance,
		EmotionalWeight: dto.EmotionalWeight, Valence: dto.Valence,
		Confidence: dto.Confidence, ExpiresAt: dto.ExpiresAt, EventAt: dto.EventAt,
		EventStart: dto.EventStart, EventEnd: dto.EventEnd, TemplateKey: dto.TemplateKey,
		SupersedesID: dto.SupersedesID, EvidenceText: dto.EvidenceText,
		EventTimePrecision: dto.EventPrecision, EventTimezone: dto.EventTimezone,
		EventTimeSource: dto.EventTimeSource, ValidFrom: dto.ValidFrom,
		ValidTo: dto.ValidTo, Qualifiers: dto.Qualifiers, Attributes: dto.Attributes,
		SourceType: memstore.SourceManual, DomainID: memstore.DomainLegacyUnassigned,
	}
}

func dashboardMemoryContext() memstore.MemoryContext {
	return memstore.MemoryContext{
		DomainID: memstore.DomainLegacyUnassigned,
		ChatKind: memstore.ChatKindAdmin,
		Purpose:  "dashboard",
	}
}

func (h *Handler) handleMemoryAddEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := decodeBody[memoryEntryRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	svc := memstore.NewMemoryService(store, memstore.DefaultConfig(""))
	res, err := svc.Add(r.Context(), dashboardMemoryContext(), dto.addInput())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.Action == "blocked_forget" {
		writeError(w, http.StatusConflict, "content was permanently forgotten")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMemoryInspectEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := store.Inspect(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMemoryUpdateEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.PathValue("id")
	dto, err := decodeBody[memoryEntryRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	old, err := store.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}
	svc := memstore.NewMemoryService(store, memstore.DefaultConfig(""))
	in := dto.addInput()
	// A body change is a correction, not an in-place overwrite: it gets a new
	// version and keeps the old evidence. Metadata-only edits stay governed too.
	if strings.TrimSpace(in.Text) != "" && strings.TrimSpace(in.Text) != old.Text {
		res, err := svc.Correct(r.Context(), dashboardMemoryContext(), id, in)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if res.Action == "blocked_forget" {
			writeError(w, http.StatusConflict, "content was permanently forgotten")
			return
		}
		writeJSON(w, http.StatusOK, res.Entry)
		return
	}
	e, err := svc.UpdateMetadata(r.Context(), dashboardMemoryContext(), id, in)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) handleMemoryCorrectEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dto, err := decodeBody[memoryEntryRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	svc := memstore.NewMemoryService(store, memstore.DefaultConfig(""))
	res, err := svc.Correct(r.Context(), dashboardMemoryContext(), r.PathValue("id"), dto.addInput())
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if res.Action == "blocked_forget" {
		writeError(w, http.StatusConflict, "content was permanently forgotten")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMemoryForgetEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dryRun := true
	expectedRevision := int64(0)
	if r.ContentLength > 0 {
		parsed, err := decodeBody[struct {
			DryRun           *bool `json:"dry_run"`
			Confirm          bool  `json:"confirm"`
			ExpectedRevision int64 `json:"expected_revision"`
		}](r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		expectedRevision = parsed.ExpectedRevision
		if parsed.Confirm {
			dryRun = false
		} else if parsed.DryRun != nil {
			dryRun = *parsed.DryRun
		}
	}
	if !dryRun && expectedRevision <= 0 {
		writeError(w, http.StatusBadRequest, "permanent forget requires confirm=true and expected_revision")
		return
	}
	svc := memstore.NewMemoryService(store, memstore.DefaultConfig(""))
	preview, err := svc.ForgetAtRevision(
		r.Context(), dashboardMemoryContext(), r.PathValue("id"), dryRun, expectedRevision)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "changed since preview") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	status := http.StatusOK
	if !dryRun && !preview.AlreadyGone {
		// The journal entry is durable before this response, but derived
		// stores are cleaned asynchronously: accepted, not "completed".
		status = http.StatusAccepted
	}
	writeJSON(w, status, preview)
}

func (h *Handler) handleMemoryDomains(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	domains, err := store.ListDomains(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": domains})
}

func (h *Handler) handleMemoryLedgerHealth(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	health, err := store.LedgerHealth(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (h *Handler) handleMemoryLedgerReplay(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	removed, err := store.ReplayForget(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (h *Handler) handleMemoryClusters(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	clusters, err := store.ListClusters(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clusters": clusters})
}

func (h *Handler) handleMemoryCreateCluster(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := decodeBody[memstore.Cluster](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := store.CreateCluster(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) handleMemoryClusterMembers(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := decodeBody[struct {
		EntryIDs []string `json:"entry_ids"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.AddClusterMembers(r.Context(), r.PathValue("id"), in.EntryIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": len(in.EntryIDs)})
}

func (h *Handler) handleMemoryGenerateSummary(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v, err := store.GenerateSummary(r.Context(), r.PathValue("id"), nil)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) handleMemoryListSummaries(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	versions, err := store.ListSummaries(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": versions})
}

func (h *Handler) handleMemorySummaryEvidence(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid summary id")
		return
	}
	entries, err := store.ExpandSummaryEvidence(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) handleMemoryCommitments(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	includeClosed := strings.EqualFold(r.URL.Query().Get("include_closed"), "true")
	list, err := store.ListCommitments(r.Context(), r.URL.Query().Get("domain_id"), includeClosed)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"commitments": list})
}

func (h *Handler) handleMemoryCreateCommitment(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := decodeBody[memstore.Commitment](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := store.CreateCommitment(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) handleMemoryCommitmentStatus(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := decodeBody[struct {
		Status   string `json:"status"`
		Evidence string `json:"evidence"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := store.UpdateCommitmentStatus(r.Context(), r.PathValue("id"), in.Status, in.Evidence)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) handleMemoryRecalls(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, err := store.ListRecalls(r.Context(), r.URL.Query().Get("domain_id"), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recalls": list})
}

func (h *Handler) handleMemoryRecallFeedback(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := decodeBody[struct {
		Feedback string `json:"feedback"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.RecordRecallFeedback(r.Context(), r.PathValue("id"), in.Feedback); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleMemoryJobs(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jobs, err := store.ListJobs(r.Context(), r.URL.Query().Get("state"), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stats, err := store.JobStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "stats": stats})
}

func (h *Handler) handleMemoryPlans(w http.ResponseWriter, r *http.Request) {
	store, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc := memstore.NewMemoryService(store, memoryConfigFrom(cfg))
	list, err := svc.ListChangePlans(r.Context(), r.URL.Query().Get("status"), 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": list})
}

func (h *Handler) handleMemoryApprovePlan(w http.ResponseWriter, r *http.Request) {
	store, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc := memstore.NewMemoryService(store, memoryConfigFrom(cfg))
	// The dashboard route is already authenticated; this is the owner context.
	mc := memstore.MemoryContext{
		DomainID: memstore.DomainLegacyUnassigned,
		ChatKind: memstore.ChatKindAdmin,
		Purpose:  "dashboard",
	}
	plan, err := svc.ApproveChangePlan(r.Context(), mc, r.PathValue("id"), "dashboard")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// memoryConfigFrom converts the loaded app config into a memstore config.
func memoryConfigFrom(cfg *config.Config) memstore.Config {
	return memstore.Config{
		Enabled:                  cfg.Memory.Enabled,
		DBPath:                   cfg.Memory.DBPath,
		BudgetTokens:             cfg.Memory.BudgetTokens,
		AutoExtract:              cfg.Memory.AutoExtract,
		PrivateChannelExclusions: cfg.Memory.PrivateChannelExclusions,
		InjectFictional:          cfg.Memory.InjectFictional,
		ReviewRequired:           cfg.Memory.ReviewRequired,
		EpisodicRetentionDays:    cfg.Memory.EpisodicRetentionDays,
	}
}

func (h *Handler) handleMemoryArchiveEntry(w http.ResponseWriter, r *http.Request) {
	h.memorySetStatus(w, r, memstore.StatusArchived)
}

func (h *Handler) handleMemoryRestoreEntry(w http.ResponseWriter, r *http.Request) {
	h.memorySetStatus(w, r, memstore.StatusActive)
}

func (h *Handler) memorySetStatus(w http.ResponseWriter, r *http.Request, status string) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc := memstore.NewMemoryService(store, memstore.DefaultConfig(""))
	if err := svc.SetStatus(r.Context(), dashboardMemoryContext(), r.PathValue("id"), status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

func (h *Handler) handleMemoryReview(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := store.Review(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) handleMemoryResolveReview(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := decodeBody[struct {
		Action   string `json:"action"`
		TargetID string `json:"target_id"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.ResolveReview(r.Context(), r.PathValue("id"), body.Action, body.TargetID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleMemoryRelationship(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rel, err := store.GetRelationship(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": rel})
}

func (h *Handler) handleMemoryUpdateRelationship(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := decodeBody[struct {
		Key        string  `json:"key"`
		Value      string  `json:"value"`
		Confidence float64 `json:"confidence"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	svc := memstore.NewMemoryService(store, memstore.DefaultConfig(""))
	if err := svc.SetRelationshipState(r.Context(), dashboardMemoryContext(), memstore.RelationshipUpdate{
		Key: body.Key, Value: body.Value, Confidence: body.Confidence,
		DomainID: memstore.DomainLegacyUnassigned, Realm: memstore.RealmReal, Audience: "private",
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleMemoryPersona(w http.ResponseWriter, r *http.Request) {
	store, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list, err := store.ListPersonas(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	active, _ := store.GetActivePersona(r.Context())
	if channel := strings.TrimSpace(r.URL.Query().Get("channel")); channel != "" {
		active, _ = store.GetActivePersonaForChannel(
			r.Context(), channel, cfg.Memory.PrivateChannelExclusions, cfg.Memory.InjectFictional,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{"personas": list, "active": active})
}

func (h *Handler) handleMemoryUpdatePersona(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := decodeBody[struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Content  string `json:"content"`
		Activate bool   `json:"activate"`
		Realm    string `json:"realm"`
		Audience string `json:"audience"`
		Scope    string `json:"scope"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ID == "" {
		body.ID = "default"
	}
	p, err := store.UpsertPersonaScoped(
		r.Context(), body.ID, body.Name, body.Content, body.Activate,
		body.Realm, body.Audience, body.Scope,
	)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// availableChannelNames lists the channel names for the settings UI: the
// built-in pico channel plus every configured adapter. The memory settings
// page renders them as opt-out checkboxes (private by default, 0.7.3).
func availableChannelNames(cfg *config.Config) []string {
	names := []string{"pico"}
	seen := map[string]bool{"pico": true}
	for name := range cfg.Channels {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names[1:])
	return names
}

func (h *Handler) handleMemorySettings(w http.ResponseWriter, r *http.Request) {
	_, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		config.MemoryConfig
		AvailableChannels []string `json:"available_channels"`
	}{cfg.Memory, availableChannelNames(cfg)})
}

func (h *Handler) handleMemoryUpdateSettings(w http.ResponseWriter, r *http.Request) {
	_, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var patch map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	baseRaw, err := json.Marshal(cfg.Memory)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(baseRaw, &merged); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for key, value := range patch {
		merged[key] = value
	}
	mergedRaw, err := json.Marshal(merged)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var body config.MemoryConfig
	strict := json.NewDecoder(strings.NewReader(string(mergedRaw)))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.DBPath == "" {
		body.DBPath = "memory/memory.db"
	}
	if body.BudgetTokens <= 0 {
		body.BudgetTokens = 1200
	}
	cfg.Memory = body
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *Handler) handleMemoryCompact(w http.ResponseWriter, r *http.Request) {
	store, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := store.Compact(r.Context(), cfg.Memory.EpisodicRetentionDays)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archived": n})
}

func (h *Handler) handleMemoryMigrationStatus(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status, err := store.MigrationStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) handleMemoryMigrationApply(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	report, err := store.ApplyMigration(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) handleMemoryOps(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	ops, err := store.ListOperations(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": ops})
}

func (h *Handler) handleMemoryCapsule(w http.ResponseWriter, r *http.Request) {
	store, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "capsule id is required")
		return
	}
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	inject := cfg.Memory.InjectFictional
	if raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("inject_fictional"))); raw == "1" || raw == "true" {
		inject = true
	}
	capsule, err := store.LoadCapsule(r.Context(), id, channel,
		cfg.Memory.PrivateChannelExclusions, inject, cfg.Memory.BudgetTokens)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, capsule)
}

func (h *Handler) handleMemoryImport(w http.ResponseWriter, r *http.Request) {
	store, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := decodeBody[struct {
		Workspace string `json:"workspace"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	source := strings.TrimSpace(body.Workspace)
	if source == "" {
		writeError(w, http.StatusBadRequest, "workspace is required")
		return
	}
	memoryDir := filepath.Join(cfg.Agents.Defaults.Workspace, "memory")
	_ = os.MkdirAll(memoryDir, 0o755)
	reportPath := filepath.Join(memoryDir, "import-report.md")
	f, ferr := os.Create(reportPath)
	if ferr != nil {
		writeError(w, http.StatusInternalServerError, ferr.Error())
		return
	}
	defer f.Close()
	report, err := store.ImportLegacy(r.Context(), source, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) handleMemoryExport(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := store.ExportEntries(r.Context(), memstore.SearchOptions{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	for _, e := range entries {
		_ = enc.Encode(e)
	}
}

func (h *Handler) handleMemoryStats(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	o, err := store.Overview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": o, "db_path": store.Path(), "db_size": o.DBSize})
}

func (h *Handler) handleMemoryRepairPreview(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filter, err := decodeBody[memstore.RepairFilter](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repair filter: "+err.Error())
		return
	}
	preview, err := store.PreviewRepair(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) handleMemoryRepairApply(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := decodeBody[struct {
		Filter memstore.RepairFilter `json:"filter"`
		Target memstore.RepairTarget `json:"target"`
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repair request: "+err.Error())
		return
	}
	updated, backup, err := store.ApplyRepair(r.Context(), body.Filter, body.Target)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated, "backup": backup})
}
