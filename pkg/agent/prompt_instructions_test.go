package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/instructions"
)

func writeInstructionsFile(t *testing.T, path string, file instructions.File) {
	t.Helper()
	if err := instructions.SaveTo(path, file); err != nil {
		t.Fatalf("SaveTo(%q) error = %v", path, err)
	}
}

func TestLoadBootstrapFilesIncludesActiveInstructions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.json")
	writeInstructionsFile(t, path, instructions.File{
		Enabled: true,
		Active:  "work",
		Presets: []instructions.Preset{
			{ID: "work", Name: "工作", Prompt: "keep it short"},
		},
	})

	cb := NewContextBuilder(t.TempDir()).WithInstructionsPath(path)
	content := cb.LoadBootstrapFiles()

	if !strings.Contains(content, "## Custom Instructions") {
		t.Fatalf("bootstrap content is missing the custom instructions heading:\n%s", content)
	}
	if !strings.Contains(content, "keep it short") {
		t.Fatalf("bootstrap content is missing the active prompt:\n%s", content)
	}
}

func TestLoadBootstrapFilesSkipsDisabledInstructions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.json")
	writeInstructionsFile(t, path, instructions.File{
		Enabled: false,
		Active:  "work",
		Presets: []instructions.Preset{
			{ID: "work", Name: "工作", Prompt: "keep it short"},
		},
	})

	cb := NewContextBuilder(t.TempDir()).WithInstructionsPath(path)
	content := cb.LoadBootstrapFiles()

	if strings.Contains(content, "## Custom Instructions") {
		t.Fatalf("disabled instructions must not be rendered:\n%s", content)
	}
	if strings.Contains(content, "keep it short") {
		t.Fatalf("disabled instructions must not leak the preset prompt:\n%s", content)
	}
}

func TestLoadBootstrapFilesSkipsMissingInstructions(t *testing.T) {
	cb := NewContextBuilder(t.TempDir()).
		WithInstructionsPath(filepath.Join(t.TempDir(), "missing.json"))

	if content := cb.LoadBootstrapFiles(); strings.Contains(content, "## Custom Instructions") {
		t.Fatalf("a missing file must not render a section:\n%s", content)
	}
}

func TestLoadBootstrapFilesSurvivesCorruptInstructions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cb := NewContextBuilder(t.TempDir()).WithInstructionsPath(path)

	// The corrupt file must degrade to "not set" rather than panic or abort
	// prompt construction.
	if content := cb.LoadBootstrapFiles(); strings.Contains(content, "## Custom Instructions") {
		t.Fatalf("a corrupt file must not render a section:\n%s", content)
	}
	if built, _ := cb.buildSystemPromptForRequest(PromptBuildRequest{}); built == "" {
		t.Fatal("prompt build produced no content for a corrupt instructions file")
	}
}

// TestInstructionChangeInvalidatesCachedPrompt covers the mechanism the whole
// feature relies on: the gateway caches one static prompt process-wide, so the
// instructions file must participate in mtime-based invalidation or a WebUI edit
// would never reach a running agent.
func TestInstructionChangeInvalidatesCachedPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.json")
	write := func(prompt string) {
		writeInstructionsFile(t, path, instructions.File{
			Enabled: true,
			Active:  "work",
			Presets: []instructions.Preset{
				{ID: "work", Name: "工作", Prompt: prompt},
			},
		})
	}

	write("first prompt")
	cb := NewContextBuilder(t.TempDir()).WithInstructionsPath(path)

	first := cb.BuildSystemPromptWithCache()
	if !strings.Contains(first, "first prompt") {
		t.Fatalf("initial cached prompt is missing the active prompt:\n%s", first)
	}

	// Ensure the rewrite lands on a strictly later mtime than the cache baseline.
	time.Sleep(20 * time.Millisecond)
	write("second prompt")

	second := cb.BuildSystemPromptWithCache()
	if !strings.Contains(second, "second prompt") {
		t.Fatalf("cached prompt was not invalidated after the instructions changed:\n%s", second)
	}
	if strings.Contains(second, "first prompt") {
		t.Fatalf("stale instructions survived cache invalidation:\n%s", second)
	}
}
