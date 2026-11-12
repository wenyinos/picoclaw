package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromMissingFileReturnsEmptyDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")

	file, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil for a missing file", err)
	}
	if file.Enabled {
		t.Fatal("missing file must load as disabled")
	}
	if len(file.Presets) != 0 {
		t.Fatalf("missing file presets = %d, want 0", len(file.Presets))
	}
	if got := file.ActivePrompt(); got != "" {
		t.Fatalf("ActivePrompt() = %q, want empty", got)
	}
}

func TestLoadFromCorruptFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := LoadFrom(path); err == nil {
		t.Fatal("LoadFrom() error = nil, want decode error")
	}
}

func TestSaveToRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.json")
	want := File{
		Enabled: true,
		Active:  "work",
		Presets: []Preset{
			{ID: "work", Name: "工作", Prompt: "简洁回答。"},
			{ID: "casual", Name: "闲聊", Prompt: "放松一点。"},
		},
	}

	if err := SaveTo(path, want); err != nil {
		t.Fatalf("SaveTo() error = %v", err)
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	if got.Enabled != want.Enabled || got.Active != want.Active {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if len(got.Presets) != len(want.Presets) {
		t.Fatalf("presets = %d, want %d", len(got.Presets), len(want.Presets))
	}
	if prompt := got.ActivePrompt(); prompt != "简洁回答。" {
		t.Fatalf("ActivePrompt() = %q, want the active preset prompt", prompt)
	}
}

func TestActivePrompt(t *testing.T) {
	presets := []Preset{
		{ID: "work", Name: "工作", Prompt: "  keep it short  "},
		{ID: "casual", Name: "闲聊", Prompt: "relax"},
	}

	tests := []struct {
		name string
		file File
		want string
	}{
		{
			name: "disabled ignores the active preset",
			file: File{Enabled: false, Active: "work", Presets: presets},
			want: "",
		},
		{
			name: "enabled returns the trimmed active prompt",
			file: File{Enabled: true, Active: "work", Presets: presets},
			want: "keep it short",
		},
		{
			name: "enabled with a blank active value yields nothing",
			file: File{Enabled: true, Active: "   ", Presets: presets},
			want: "",
		},
		{
			name: "enabled with an unknown active preset yields nothing",
			file: File{Enabled: true, Active: "ghost", Presets: presets},
			want: "",
		},
		{
			name: "empty preset list yields nothing",
			file: File{Enabled: true, Active: "work"},
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.file.ActivePrompt(); got != test.want {
				t.Fatalf("ActivePrompt() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	validPreset := Preset{ID: "work", Name: "工作", Prompt: "prompt"}

	tests := []struct {
		name    string
		file    File
		wantErr string
	}{
		{
			name: "disabled file with no presets is valid",
			file: File{},
		},
		{
			name: "enabled file with a matching active preset is valid",
			file: File{Enabled: true, Active: "work", Presets: []Preset{validPreset}},
		},
		{
			name: "disabled file may keep a stale active value",
			file: File{Enabled: false, Active: "ghost", Presets: []Preset{validPreset}},
		},
		{
			name:    "too many presets",
			file:    File{Presets: manyPresets(MaxPresets + 1)},
			wantErr: "at most",
		},
		{
			name:    "invalid preset id",
			file:    File{Presets: []Preset{{ID: "Bad ID", Name: "n", Prompt: "p"}}},
			wantErr: "must match",
		},
		{
			name: "duplicate preset id",
			file: File{Presets: []Preset{
				{ID: "dup", Name: "a", Prompt: "p"},
				{ID: "dup", Name: "b", Prompt: "q"},
			}},
			wantErr: "duplicate id",
		},
		{
			name:    "missing name",
			file:    File{Presets: []Preset{{ID: "work", Name: "  ", Prompt: "p"}}},
			wantErr: "name is required",
		},
		{
			name:    "missing prompt",
			file:    File{Presets: []Preset{{ID: "work", Name: "n", Prompt: "  "}}},
			wantErr: "prompt is required",
		},
		{
			name: "prompt exceeds the rune limit",
			file: File{Presets: []Preset{{
				ID:     "work",
				Name:   "n",
				Prompt: strings.Repeat("字", MaxPromptRunes+1),
			}}},
			wantErr: "exceeds",
		},
		{
			name:    "enabled without an active preset",
			file:    File{Enabled: true, Presets: []Preset{validPreset}},
			wantErr: "active preset is required",
		},
		{
			name:    "enabled with an unknown active preset",
			file:    File{Enabled: true, Active: "ghost", Presets: []Preset{validPreset}},
			wantErr: "does not exist",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.file.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want an error containing %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestSaveToRejectsInvalidDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.json")

	err := SaveTo(path, File{Enabled: true, Presets: []Preset{{ID: "work", Name: "n", Prompt: "p"}}})
	if err == nil {
		t.Fatal("SaveTo() error = nil, want a validation error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatal("SaveTo() must not create a file when validation fails")
	}
}

func manyPresets(count int) []Preset {
	presets := make([]Preset, 0, count)
	for i := 0; i < count; i++ {
		presets = append(presets, Preset{
			ID:     "p" + string(rune('a'+i%26)) + "-" + strings.Repeat("x", i/26),
			Name:   "n",
			Prompt: "p",
		})
	}
	return presets
}
