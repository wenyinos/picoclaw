package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sipeed/picoclaw/pkg/instructions"
)

// registerInstructionRoutes binds the operator system-prompt overlay endpoints.
//
// Access control is inherited from the launcher dashboard auth middleware that
// wraps the whole mux, so these handlers do not check auth themselves.
func (h *Handler) registerInstructionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instructions", h.handleGetInstructions)
	mux.HandleFunc("PUT /api/instructions", h.handleUpdateInstructions)
}

// handleGetInstructions returns the current instructions file.
//
// A missing file yields an empty disabled document rather than a 404: the WebUI
// renders the same empty state for "never configured" and "file deleted".
//
//	GET /api/instructions
func (h *Handler) handleGetInstructions(w http.ResponseWriter, r *http.Request) {
	file, err := instructions.Load()
	if err != nil {
		// Surface a corrupt file instead of silently returning an empty config,
		// otherwise the operator would believe their edits are being applied.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("failed to read instructions: %v", err),
			"path":  instructions.Path(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"enabled": file.Enabled,
		"active":  file.Active,
		"presets": file.Presets,
		"path":    instructions.Path(),
		"limits": map[string]any{
			"max_presets":      instructions.MaxPresets,
			"max_prompt_runes": instructions.MaxPromptRunes,
			"max_name_runes":   instructions.MaxNameRunes,
		},
	})
}

// handleUpdateInstructions validates and persists the instructions file.
//
//	PUT /api/instructions
func (h *Handler) handleUpdateInstructions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var file instructions.File
	if err = json.Unmarshal(body, &file); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "validation_error",
			"errors": []string{fmt.Sprintf("Invalid JSON: %v", err)},
		})
		return
	}

	if err = file.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "validation_error",
			"errors": []string{err.Error()},
		})
		return
	}

	if err = instructions.Save(file); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save instructions: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"path":   instructions.Path(),
	})
}
