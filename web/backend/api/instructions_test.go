package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newInstructionsTestMux points PICOCLAW_HOME at a temporary directory so the
// handlers read and write an isolated instructions.json instead of the real
// user home. config.GetHome() re-reads the environment on every call, so this
// also covers instructions.Path() resolving inside the handler.
func newInstructionsTestMux(t *testing.T) (mux *http.ServeMux, path string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("PICOCLAW_HOME", home)

	handler := NewHandler(filepath.Join(home, "config.json"))
	mux = http.NewServeMux()
	handler.registerInstructionRoutes(mux)

	return mux, filepath.Join(home, "instructions.json")
}

// TestInstructionsAPI_FirstSaveCreatesFile is the acceptance test for the WebUI
// flow: opening the page must not touch the filesystem, and the first save must
// create a readable 0600 file.
func TestInstructionsAPI_FirstSaveCreatesFile(t *testing.T) {
	mux, path := newInstructionsTestMux(t)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("instructions.json must not exist before the first save")
	}

	rec := doJSON(t, mux, http.MethodGet, "/api/instructions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("GET must not create the file")
	}

	var initial struct {
		Enabled bool `json:"enabled"`
		Active  string
		Presets []any `json:"presets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode GET body: %v", err)
	}
	if initial.Enabled || initial.Active != "" || len(initial.Presets) != 0 {
		t.Fatalf("a fresh home must render an empty document, got %s", rec.Body.String())
	}

	rec = doJSON(t, mux, http.MethodPut, "/api/instructions", map[string]any{
		"enabled": true,
		"active":  "work",
		"presets": []map[string]any{
			{"id": "work", "name": "工作模式", "prompt": "先给结论，再给依据。"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", rec.Code, rec.Body.String())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("instructions.json was not created by the first save: %v", err)
	}
	if perms := info.Mode().Perm(); perms != 0o600 {
		t.Fatalf("file mode = %o, want 600", perms)
	}

	rec = doJSON(t, mux, http.MethodGet, "/api/instructions", nil)
	var saved struct {
		Enabled bool   `json:"enabled"`
		Active  string `json:"active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode saved body: %v", err)
	}
	if !saved.Enabled || saved.Active != "work" {
		t.Fatalf("saved document did not round-trip: %s", rec.Body.String())
	}
}

func TestInstructionsAPI_RejectsInvalidPayloadWithoutWriting(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "blank prompt",
			body: map[string]any{
				"enabled": true, "active": "work",
				"presets": []map[string]any{{"id": "work", "name": "n", "prompt": "   "}},
			},
		},
		{
			name: "uppercase or spaced id",
			body: map[string]any{
				"enabled": true, "active": "Work Mode",
				"presets": []map[string]any{{"id": "Work Mode", "name": "n", "prompt": "p"}},
			},
		},
		{
			name: "enabled without an active preset",
			body: map[string]any{
				"enabled": true, "active": "",
				"presets": []map[string]any{{"id": "work", "name": "n", "prompt": "p"}},
			},
		},
		{
			name: "active preset does not exist",
			body: map[string]any{
				"enabled": true, "active": "ghost",
				"presets": []map[string]any{{"id": "work", "name": "n", "prompt": "p"}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux, path := newInstructionsTestMux(t)

			rec := doJSON(t, mux, http.MethodPut, "/api/instructions", test.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT status = %d, want 400, body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "validation_error") {
				t.Fatalf("PUT body = %s, want a validation_error payload", rec.Body.String())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("a rejected payload must not create the file")
			}
		})
	}
}

func TestInstructionsAPI_RejectsMalformedJSON(t *testing.T) {
	mux, path := newInstructionsTestMux(t)

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/instructions",
		strings.NewReader(`{"enabled": tru`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("malformed JSON must not create the file")
	}
}

// TestInstructionsAPI_DisabledDocumentIsAccepted guards the "save a disabled
// config" path: turning the feature off must still persist, otherwise the
// operator could never undo an enable.
func TestInstructionsAPI_DisabledDocumentIsAccepted(t *testing.T) {
	mux, path := newInstructionsTestMux(t)

	rec := doJSON(t, mux, http.MethodPut, "/api/instructions", map[string]any{
		"enabled": false,
		"active":  "",
		"presets": []map[string]any{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a disabled document must still be persisted: %v", err)
	}
}
