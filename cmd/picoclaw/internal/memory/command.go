package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memstore"
)

// NewMemoryCommand returns the `picoclaw memory` command group.
func NewMemoryCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage long-term memory (SQLite)",
	}
	cmd.AddCommand(
		newMigrateCommand(),
		newRepairCommand(),
		newInspectCommand(),
		newRebuildFTSCommand(),
		newForgetCommand(),
		newReplayForgetCommand(),
		newCapsuleCommand(),
		newImportCommand(),
		newAuditCommand(),
		newStatsCommand(),
		newCompactCommand(),
		newExportCommand(),
		newRenderCommand(),
	)
	return cmd
}

func newMigrateCommand() *cobra.Command {
	var backup bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Upgrade memory.db through schema v2 to v3 and repair data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			if !backup {
				return fmt.Errorf("migration always creates a VACUUM INTO backup; remove --backup=false")
			}
			report, err := store.ApplyMigration(context.Background())
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		},
	}
	cmd.Flags().BoolVar(&backup, "backup", true, "create a VACUUM INTO backup before migrating")
	return cmd
}

func newRepairCommand() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Apply the idempotent v3 data repairs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if dryRun {
				preview, err := store.RepairPreviewData(context.Background())
				if err != nil {
					return err
				}
				return enc.Encode(preview)
			}
			report, err := store.RepairData(context.Background())
			if err != nil {
				return err
			}
			return enc.Encode(report)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "only preview the repair impact")
	return cmd
}

func newInspectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <id>",
		Short: "Show the evidence chain for one memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			res, err := store.Inspect(context.Background(), args[0])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(res)
		},
	}
}

func newRebuildFTSCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild-fts",
		Short: "Rebuild the full-text index from entries",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			n, err := store.RebuildFTS(context.Background())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "indexed %d entries\n", n)
			return nil
		},
	}
}

func newForgetCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "forget <id>",
		Short: "Hard-delete a memory and its derived data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			preview, err := store.Forget(context.Background(), args[0], !yes)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(preview)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "actually delete; without it this is a dry run")
	return cmd
}

func newReplayForgetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "replay-forget",
		Short: "Re-apply forget tombstones after restoring a backup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			n, err := store.ReplayForget(context.Background())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %d forgotten entries\n", n)
			return nil
		},
	}
}

func newCapsuleCommand() *cobra.Command {
	var preset string
	cmd := &cobra.Command{
		Use:   "capsule",
		Short: "Print a scene capsule and its token count",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, cfg, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			clusterID := "capsule:fictional_t0"
			switch preset {
			case "t0_compact":
				clusterID = "capsule:fictional_t0_compact"
			case "t0", "":
			default:
				return fmt.Errorf("unknown preset %q (want t0 or t0_compact)", preset)
			}
			capsule, err := store.LoadCapsule(context.Background(), clusterID, "pico",
				cfg.Memory.PrivateChannels, true, cfg.Memory.BudgetTokens)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), capsule.CapsuleText())
			fmt.Fprintf(cmd.OutOrStdout(), "\ntokens=%d items=%d truncated=%v\n",
				capsule.TotalTokens, len(capsule.Items), capsule.Truncated)
			return nil
		},
	}
	cmd.Flags().StringVar(&preset, "preset", "t0", "capsule preset: t0 or t0_compact")
	return cmd
}

func openStore() (*memstore.Store, *config.Config, error) {
	cfg, err := internal.LoadConfig()
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

func newImportCommand() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a 0.3.x workspace (task records + session summaries)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" {
				return fmt.Errorf("--from is required")
			}
			store, cfg, err := openStore()
			if err != nil {
				return err
			}
			reportPath := filepath.Join(cfg.Agents.Defaults.Workspace, "memory", "import-report.md")
			if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
				return err
			}
			f, err := os.Create(reportPath)
			if err != nil {
				return err
			}
			defer f.Close()
			report, err := store.ImportLegacy(context.Background(), from, f)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "workspace directory to import from")
	return cmd
}

func newAuditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "Show memory counters, budgets, FTS and migration state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, cfg, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			ctx := context.Background()
			o, err := store.Overview(ctx)
			if err != nil {
				return err
			}
			status, err := store.MigrationStatus(ctx)
			if err != nil {
				return err
			}
			fixedBudget := cfg.Memory.BudgetTokens * 45 / 100
			fixed, usage := store.BuildFixedContext(ctx, fixedBudget, cfg.Memory.InjectFictional,
				"pico", cfg.Memory.PrivateChannels)
			retrievalBudget := cfg.Memory.BudgetTokens - usage.TotalTokens
			if retrievalBudget < 200 {
				retrievalBudget = 200
			}
			retrieval := store.BuildRetrievalContext(ctx, "本质定调", "pico", retrievalBudget,
				cfg.Memory.InjectFictional, cfg.Memory.PrivateChannels)
			var uncategorized int
			if entries, err := store.List(ctx, memstore.SearchOptions{
				Status: memstore.StatusActive, Predicate: "待分类", Limit: 500,
			}); err == nil {
				uncategorized = len(entries)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"total=%d active=%d pending=%d superseded=%d archived=%d db=%d bytes revision=%d\n",
				o.Total, o.Active, o.Pending, o.Superseded, o.Archived, o.DBSize, o.Revision)
			fmt.Fprintf(cmd.OutOrStdout(),
				"schema=%d tokenizer=%s fts_rows=%d relations=%d pinned=%d backups=%d\n",
				status.SchemaVersion, status.FTSTokenizer, status.FTSRows,
				status.Relations, status.PinnedCore, store.BackupCount())
			fmt.Fprintf(cmd.OutOrStdout(),
				"uncategorized_active=%d empty_predicate=%d empty_relationship_facet=%d\n",
				uncategorized, status.EmptyPredicate, status.EmptyRelationshipFacet)
			fmt.Fprintf(cmd.OutOrStdout(),
				"fixed_tokens=%d base_tokens=%d boundary_tokens=%d boundary_budget=%d boundary_cut=%d retrieval_tokens=%d fixed_chars=%d\n",
				usage.TotalTokens, usage.BaseTokens, usage.BoundaryTokens, usage.BoundaryBudget,
				usage.BoundaryCut, memstore.EstimateTokens(retrieval), len(fixed))
			return nil
		},
	}
}

func newStatsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Print detailed memory statistics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			o, err := store.Overview(context.Background())
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(o)
		},
	}
}

func newCompactCommand() *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Archive expired and stale memories",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, cfg, err := openStore()
			if err != nil {
				return err
			}
			if days <= 0 {
				days = cfg.Memory.EpisodicRetentionDays
			}
			n, err := store.Compact(context.Background(), days)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "archived %d entries\n", n)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "retention-days", 0, "episodic retention days")
	return cmd
}

func newExportCommand() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export memories as JSONL",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			entries, err := store.List(context.Background(), memstore.SearchOptions{Limit: 100000})
			if err != nil {
				return err
			}
			var f *os.File
			if out == "" {
				f = os.Stdout
			} else {
				f, err = os.Create(out)
				if err != nil {
					return err
				}
				defer f.Close()
			}
			enc := json.NewEncoder(f)
			for _, e := range entries {
				if err := enc.Encode(e); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "output file (default stdout)")
	return cmd
}

func newRenderCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "render",
		Short: "Regenerate MEMORY.md from the SQLite store",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, cfg, err := openStore()
			if err != nil {
				return err
			}
			ctx := context.Background()
			content, _ := store.BuildFixedContext(ctx, cfg.Memory.BudgetTokens, cfg.Memory.InjectFictional,
				"pico", []string{"pico"})
			entries, _ := store.List(ctx, memstore.SearchOptions{Status: memstore.StatusActive, Limit: 100})
			var b strings.Builder
			if content == "" {
				b.WriteString("# Memory\n\n")
			} else {
				b.WriteString(content)
				b.WriteString("\n\n")
			}
			if len(entries) > 0 {
				b.WriteString("## 记忆条目\n")
				for _, e := range entries {
					b.WriteString("- [" + e.Layer + "/" + e.Realm + "] " + strings.TrimSpace(e.Text) + "\n")
				}
			}
			if content == "" && len(entries) == 0 {
				b.WriteString("(empty)\n")
			}
			path := filepath.Join(cfg.Agents.Defaults.Workspace, "memory", "MEMORY.md")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}
