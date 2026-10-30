package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func newMemoryTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	cfgPath := filepath.Join(tmp, "config.json")
	cfg := map[string]any{
		"version": 3,
		"agents": map[string]any{
			"defaults": map[string]any{"workspace": workspace},
		},
		"memory": map[string]any{
			"enabled":          true,
			"db_path":          "memory/memory.db",
			"budget_tokens":    1200,
			"auto_extract":     false,
			"review_required":  true,
			"private_channels": []string{"pico"},
			"inject_fictional": true,
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return NewHandler(cfgPath), cfgPath
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestMemoryAPI_EntryLifecycle(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)

	rec := doJSON(t, mux, http.MethodPost, "/api/memory/entries", map[string]any{
		"text": "用户对花生过敏", "layer": "profile", "importance": 0.95,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var added struct {
		Action string `json:"action"`
		Entry  struct {
			ID string `json:"id"`
		} `json:"entry"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.Action != "added" || added.Entry.ID == "" {
		t.Fatalf("unexpected add response: %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodGet, "/api/memory/entries?limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list struct {
		Entries []struct {
			Text string `json:"text"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Entries) != 1 || list.Entries[0].Text != "用户对花生过敏" {
		t.Fatalf("unexpected list: %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPost, "/api/memory/entries/"+added.Entry.ID+"/archive", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive status = %d", rec.Code)
	}
}

func TestMemoryAPI_RelationshipPersonaSettings(t *testing.T) {
	h, cfgPath := newMemoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)

	rec := doJSON(t, mux, http.MethodPut, "/api/memory/relationship",
		map[string]any{"key": "ai_name", "value": "小P", "confidence": 0.9})
	if rec.Code != http.StatusOK {
		t.Fatalf("relationship status = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, mux, http.MethodGet, "/api/memory/relationship", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("小P")) {
		t.Fatalf("relationship get: %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPut, "/api/memory/persona",
		map[string]any{
			"id": "default", "name": "Pico", "content": "冷静、简洁",
			"activate": true, "realm": "fictional", "audience": "private", "scope": "user",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("persona status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"realm":"fictional"`)) {
		t.Fatalf("persona scope missing: %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodGet, "/api/memory/persona?channel=pico", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"realm":"fictional"`)) {
		t.Fatalf("private persona missing for pico: %s", rec.Body.String())
	}
	rec = doJSON(t, mux, http.MethodGet, "/api/memory/persona?channel=public", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("public persona status = %d", rec.Code)
	}
	var publicPersona struct {
		Active *struct {
			ID string `json:"id"`
		} `json:"active"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &publicPersona)
	if publicPersona.Active != nil {
		t.Fatalf("private persona leaked into public channel: %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPut, "/api/memory/settings", map[string]any{
		"enabled": true, "db_path": "memory/memory.db", "budget_tokens": 900,
		"auto_extract": true, "review_required": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, body=%s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(cfgPath)
	if !bytes.Contains(data, []byte(`"budget_tokens": 900`)) {
		t.Fatalf("settings not persisted: %s", data)
	}

	rec = doJSON(t, mux, http.MethodGet, "/api/memory/overview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status = %d", rec.Code)
	}
}

func TestMemoryAPI_MigrationStatusAndApply(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)

	rec := doJSON(t, mux, http.MethodGet, "/api/memory/migration/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var status struct {
		SchemaVersion int `json:"schema_version"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if status.SchemaVersion != 2 {
		t.Fatalf("schema = %d, want 2: %s", status.SchemaVersion, rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPost, "/api/memory/migration/apply", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d, body=%s", rec.Code, rec.Body.String())
	}
	var report struct {
		SchemaVersion int    `json:"schema_version"`
		Backup        string `json:"backup"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &report)
	if report.SchemaVersion != 2 || report.Backup == "" {
		t.Fatalf("unexpected report: %s", rec.Body.String())
	}
	if _, err := os.Stat(report.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestMemoryAPI_RepairReclassify(t *testing.T) {
	h, _ := newMemoryTestHandler(t)
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)

	for _, text := range []string{"姐姐的角色设定：蓝色海豚", "token 的不同含义"} {
		rec := doJSON(t, mux, http.MethodPost, "/api/memory/entries", map[string]any{
			"text": text, "realm": "work", "layer": "procedural", "importance": 0.6,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("add status = %d, body=%s", rec.Code, rec.Body.String())
		}
	}

	filter := map[string]any{"realm": "work", "layer": "procedural", "query": "蓝色海豚"}
	rec := doJSON(t, mux, http.MethodPost, "/api/memory/repair/preview", filter)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &preview)
	if preview.Count != 1 {
		t.Fatalf("preview count = %d, want 1: %s", preview.Count, rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPost, "/api/memory/repair/apply", map[string]any{
		"filter": filter,
		"target": map[string]any{"realm": "fictional", "layer": "procedural", "audience": "private"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var applied struct {
		Updated int    `json:"updated"`
		Backup  string `json:"backup"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &applied)
	if applied.Updated != 1 || applied.Backup == "" {
		t.Fatalf("unexpected apply response: %s", rec.Body.String())
	}
	if _, err := os.Stat(applied.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	rec = doJSON(t, mux, http.MethodGet, "/api/memory/entries?realm=fictional&limit=10", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("蓝色海豚")) {
		t.Fatalf("reclassified entry missing: %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPost, "/api/memory/repair/apply", map[string]any{
		"filter": map[string]any{},
		"target": map[string]any{"realm": "fictional", "layer": "procedural"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty filter status = %d, want 400", rec.Code)
	}
}
