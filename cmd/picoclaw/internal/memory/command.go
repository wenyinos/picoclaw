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
		Short: "Upgrade memory.db to schema v2 and normalize empty predicates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			if !backup {
				return fmt.Errorf("migration always creates a VACUUM INTO backup; remove --backup=false")
			}
			report, err := store.ApplyV2Migration(context.Background())
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
		Short: "Show memory counters and pending reviews",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			o, err := store.Overview(context.Background())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"total=%d active=%d pending=%d superseded=%d archived=%d db=%d bytes revision=%d\n",
				o.Total, o.Active, o.Pending, o.Superseded, o.Archived, o.DBSize, o.Revision)
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
			content := store.BuildFixedContext(ctx, cfg.Memory.BudgetTokens, cfg.Memory.InjectFictional,
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
