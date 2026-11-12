// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package instructions stores the operator-configured system prompt overlay.
//
// The data lives in its own file instead of config.json on purpose: the agent
// process reads it through ContextBuilder's mtime-driven prompt cache, so a
// WebUI edit takes effect on the next turn across every channel without a
// restart. Editing config.json would not be observed by a running agent.
package instructions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
)

const (
	// FileName is the on-disk file name inside the picoclaw home directory.
	FileName = "instructions.json"

	// MaxPresets caps the preset list. Beyond roughly ten entries the operator
	// wants separate agents, not more presets.
	MaxPresets = 10

	// MaxPromptRunes caps a single preset's prompt length.
	MaxPromptRunes = 8000

	// MaxNameRunes caps a preset's display name.
	MaxNameRunes = 60
)

var presetIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// Preset is one selectable system prompt overlay.
type Preset struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// File is the full contents of instructions.json.
type File struct {
	Enabled bool     `json:"enabled"`
	Active  string   `json:"active"`
	Presets []Preset `json:"presets"`
}

// Path returns the absolute path of the instructions file.
func Path() string {
	return filepath.Join(config.GetHome(), FileName)
}

// Load reads the instructions file.
//
// A missing file is not an error: it yields an empty (disabled) File so that
// "never configured", "explicitly deleted", and "explicitly disabled" all
// behave identically.
func Load() (File, error) {
	return LoadFrom(Path())
}

// LoadFrom reads an instructions file from an explicit path.
func LoadFrom(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, err
	}

	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if file.Presets == nil {
		file.Presets = []Preset{}
	}
	return file, nil
}

// Save validates and writes the instructions file atomically.
func Save(file File) error {
	return SaveTo(Path(), file)
}

// SaveTo validates and writes to an explicit path atomically.
func SaveTo(path string, file File) error {
	if err := file.Validate(); err != nil {
		return err
	}
	if file.Presets == nil {
		file.Presets = []Preset{}
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

// ActivePrompt returns the prompt text that should be injected into the system
// prompt. It returns "" whenever instructions are disabled, unset, or the
// active preset cannot be resolved. Callers MUST treat "" as "inject nothing".
func (f File) ActivePrompt() string {
	if !f.Enabled {
		return ""
	}

	active := strings.TrimSpace(f.Active)
	if active == "" {
		return ""
	}
	for _, preset := range f.Presets {
		if preset.ID == active {
			return strings.TrimSpace(preset.Prompt)
		}
	}
	return ""
}

// Validate reports whether the file may be saved.
//
// An enabled file must point at an existing preset, and every preset must carry
// a non-empty prompt: an enabled-but-empty state is exactly the ambiguity this
// validation exists to prevent.
func (f File) Validate() error {
	if len(f.Presets) > MaxPresets {
		return fmt.Errorf("at most %d presets are allowed, got %d", MaxPresets, len(f.Presets))
	}

	seen := make(map[string]struct{}, len(f.Presets))
	for i, preset := range f.Presets {
		position := i + 1
		if !presetIDPattern.MatchString(preset.ID) {
			return fmt.Errorf(
				"preset #%d: id %q must match %s",
				position, preset.ID, presetIDPattern.String(),
			)
		}
		if _, duplicate := seen[preset.ID]; duplicate {
			return fmt.Errorf("preset #%d: duplicate id %q", position, preset.ID)
		}
		seen[preset.ID] = struct{}{}

		name := strings.TrimSpace(preset.Name)
		if name == "" {
			return fmt.Errorf("preset %q: name is required", preset.ID)
		}
		if utf8.RuneCountInString(name) > MaxNameRunes {
			return fmt.Errorf("preset %q: name exceeds %d characters", preset.ID, MaxNameRunes)
		}

		if strings.TrimSpace(preset.Prompt) == "" {
			return fmt.Errorf("preset %q: prompt is required", preset.ID)
		}
		if utf8.RuneCountInString(preset.Prompt) > MaxPromptRunes {
			return fmt.Errorf("preset %q: prompt exceeds %d characters", preset.ID, MaxPromptRunes)
		}
	}

	if !f.Enabled {
		return nil
	}
	if strings.TrimSpace(f.Active) == "" {
		return errors.New("an active preset is required when instructions are enabled")
	}
	if _, ok := seen[f.Active]; !ok {
		return fmt.Errorf("active preset %q does not exist", f.Active)
	}
	return nil
}
