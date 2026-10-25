package memstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type legacyTask struct {
	ID         string `json:"id"`
	Summary    string `json:"summary"`
	Status     string `json:"status"`
	Success    bool   `json:"success"`
	SessionKey string `json:"session_key"`
}

// ImportLegacy performs a one-shot import from a PicoClaw 0.3.x workspace.
// It is deliberately not a runtime migration: 0.4 treats memory.db as the
// source of truth and old MEMORY.md files are ignored.
func (s *Store) ImportLegacy(ctx context.Context, workspace string, report io.Writer) (ImportReport, error) {
	var r ImportReport
	taskPath := filepath.Join(workspace, "state", "evolution", "task-records.jsonl")
	if f, err := os.Open(taskPath); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			r.TaskRows++
			var task legacyTask
			if err := json.Unmarshal([]byte(line), &task); err != nil {
				continue
			}
			summary := strings.TrimSpace(task.Summary)
			if summary == "" {
				continue
			}
			if IsChitchat(summary) {
				r.ChitchatSkipped++
				continue
			}
			template := TemplateKeyForSummary(summary)
			realm := RealmWork
			layer := LayerProcedural
			importance := 0.6
			status := StatusActive
			if template != "" {
				r.TemplateFolded++
				switch template {
				case "memory_extract_prompt_v1":
					// This is a recurring internal instruction, not a memory.
					continue
				case "pinned_memory_prefix_v1":
					realm, layer, status, importance = RealmUnknown, LayerProfile, StatusPending, 0.5
				case "roleplay_prefix_v1":
					realm, layer, status, importance = RealmFictional, LayerEpisodic, StatusPending, 0.3
				}
			}
			if !task.Success && template == "" {
				importance = 0.25
				status = StatusArchived
				r.FailuresArchived++
			}
			res, err := s.Add(ctx, AddInput{
				Text: summary, Realm: realm, Layer: layer,
				Importance: importance, Confidence: 0.6, TemplateKey: template,
				ForceStatus: status, SourceType: "task_record", SourceID: task.ID,
				SessionKey: task.SessionKey,
			})
			if err != nil {
				continue
			}
			if res.Action == "added" || res.Action == "superseded" {
				r.TaskImported++
			} else {
				r.TaskMerged++
			}
			r.TaskUnique = r.TaskImported
		}
		if err := scanner.Err(); err != nil {
			return r, fmt.Errorf("import task records: %w", err)
		}
	}

	metaFiles, _ := filepath.Glob(filepath.Join(workspace, "sessions", "*.meta.json"))
	for _, path := range metaFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta struct {
			Key     string `json:"key"`
			Summary string `json:"summary"`
		}
		if json.Unmarshal(data, &meta) != nil {
			continue
		}
		summary := strings.TrimSpace(meta.Summary)
		if summary == "" {
			continue
		}
		res, err := s.Add(ctx, AddInput{
			Text: summary, Realm: RealmUnknown, Layer: LayerEpisodic,
			Importance: 0.5, Confidence: 0.4, ForceStatus: StatusPending,
			SourceType: "session_summary", SourceID: strings.TrimSuffix(filepath.Base(path), ".meta.json"),
			SessionKey: meta.Key,
		})
		if err != nil {
			continue
		}
		if res.Action == "added" || res.Action == "superseded" {
			r.SummariesImported++
		} else {
			r.SummariesMerged++
		}
	}

	// Legacy MEMORY.md bullets become reviewable profile candidates.
	if data, err := os.ReadFile(filepath.Join(workspace, "memory", "MEMORY.md")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- ") {
				continue
			}
			text := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if text == "" || isLegacyPlaceholder(text) {
				continue
			}
			if res, err := s.Add(ctx, AddInput{
				Text: text, Realm: RealmUnknown, Layer: LayerProfile,
				Importance: 0.7, Confidence: 0.5, ForceStatus: StatusPending,
				SourceType: "legacy_memory_md",
			}); err == nil && (res.Action == "added" || res.Action == "duplicate") {
				r.LegacyNotes++
			}
		}
	}

	// Reclaim the WAL produced by the bulk import so the shipped workspace is
	// compact.
	_, _ = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")

	if report != nil {
		fmt.Fprintf(report, "# Memory Import Report\n\n")
		fmt.Fprintf(report, "- task rows: %d\n- unique/imported: %d\n- merged: %d\n", r.TaskRows, r.TaskImported, r.TaskMerged)
		fmt.Fprintf(report, "- template folded: %d\n- chitchat skipped: %d\n- failures archived: %d\n", r.TemplateFolded, r.ChitchatSkipped, r.FailuresArchived)
		fmt.Fprintf(report, "- summaries imported: %d (merged %d)\n", r.SummariesImported, r.SummariesMerged)
		fmt.Fprintf(report, "- legacy MEMORY.md notes: %d\n", r.LegacyNotes)
	}
	return r, nil
}

func isLegacyPlaceholder(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"preferences learned over time", "model preferences", "channel settings",
		"skills enabled", "this file stores", "user information goes here",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
