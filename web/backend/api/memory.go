package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	mux.HandleFunc("PUT /api/memory/entries/{id}", h.handleMemoryUpdateEntry)
	mux.HandleFunc("POST /api/memory/entries/{id}/archive", h.handleMemoryArchiveEntry)
	mux.HandleFunc("POST /api/memory/entries/{id}/restore", h.handleMemoryRestoreEntry)
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
	mux.HandleFunc("POST /api/memory/reindex", h.handleMemoryReindex)
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
		Enabled:               cfg.Memory.Enabled,
		DBPath:                cfg.Memory.DBPath,
		BudgetTokens:          cfg.Memory.BudgetTokens,
		AutoExtract:           cfg.Memory.AutoExtract,
		PrivateChannels:       cfg.Memory.PrivateChannels,
		InjectFictional:       cfg.Memory.InjectFictional,
		ReviewRequired:        cfg.Memory.ReviewRequired,
		EpisodicRetentionDays: cfg.Memory.EpisodicRetentionDays,
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
	err = json.Unmarshal(body, &v)
	return v, err
}

func (h *Handler) handleMemoryAddEntry(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	in, err := decodeBody[memstore.AddInput](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := store.Add(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	in, err := decodeBody[memstore.AddInput](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := store.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
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
	if err := store.SetStatus(r.Context(), r.PathValue("id"), status); err != nil {
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
	if err := store.SetRelationship(r.Context(), body.Key, body.Value, body.Confidence); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleMemoryPersona(w http.ResponseWriter, r *http.Request) {
	store, _, err := h.memoryStore()
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
	}](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ID == "" {
		body.ID = "default"
	}
	p, err := store.UpsertPersona(r.Context(), body.ID, body.Name, body.Content, body.Activate)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) handleMemorySettings(w http.ResponseWriter, r *http.Request) {
	_, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg.Memory)
}

func (h *Handler) handleMemoryUpdateSettings(w http.ResponseWriter, r *http.Request) {
	_, cfg, err := h.memoryStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := decodeBody[config.MemoryConfig](r)
	if err != nil {
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

func (h *Handler) handleMemoryReindex(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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
	entries, err := store.List(r.Context(), memstore.SearchOptions{Limit: 10000})
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
