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
	sessionmem "github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/pid"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

type cliCleanerGroup []memstore.MessageCleaner

func (g cliCleanerGroup) CleanLocators(ctx context.Context, locators []memstore.SourceLocator) (memstore.CleanupOutcome, error) {
	total := memstore.CleanupOutcome{}
	for _, cleaner := range g {
		outcome, err := cleaner.CleanLocators(ctx, locators)
		if err != nil {
			total.Unavailable += len(locators)
			total.Details = append(total.Details, err.Error())
			continue
		}
		total.Removed += outcome.Removed
		total.AlreadyAbsent += outcome.AlreadyAbsent
		total.Unavailable += outcome.Unavailable
		total.OutcomeUnknown += outcome.OutcomeUnknown
		total.Sessions += outcome.Sessions
		total.Details = append(total.Details, outcome.Details...)
	}
	return total, nil
}

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
		newForgetAuditCommand(),
		newReplayForgetCommand(),
		newCleanupCommand(),
		newLedgerVerifyCommand(),
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
		Short: "Upgrade memory.db from schema v3 to v4 with a backup",
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
		Short: "Heal missing v4 columns, tables, indexes and the FTS projection",
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
	var expectedRevision int64
	cmd := &cobra.Command{
		Use:   "forget <id>",
		Short: "Hard-delete a memory and its derived data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if yes && expectedRevision <= 0 {
				return fmt.Errorf("--yes requires --expected-revision from a fresh preview")
			}
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			preview, err := store.ForgetAtRevision(
				cliAuditContext(store), args[0], !yes, expectedRevision)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(preview)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "actually delete; without it this is a dry run")
	cmd.Flags().Int64Var(&expectedRevision, "expected-revision", 0, "entry revision from the deletion preview")
	return cmd
}

func newForgetAuditCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "forget-audit",
		Short: "Report historical forget operations without deleting data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := store.AuditHistoricalForgets(context.Background(), limit)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 1000, "maximum historical operations to report (1-5000)")
	return cmd
}

func newReplayForgetCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "replay-forget",
		Short: "Re-apply forget tombstones after restoring a backup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			ledger := store.ForgetLedgerRef()
			if ledger == nil {
				return fmt.Errorf("forget ledger unavailable")
			}
			if !yes {
				intents, err := ledger.AllIntents(context.Background())
				if err != nil {
					return err
				}
				entries := 0
				for _, intent := range intents {
					manifest, err := memstore.ParseManifest(intent.TargetManifest)
					if err != nil || len(manifest.EntryIDs) == 0 {
						continue
					}
					entries += len(manifest.EntryIDs)
				}
				fmt.Fprintf(cmd.OutOrStdout(),
					"dry run: %d intent(s), %d manifest entry target(s); pass --yes to execute\n",
					len(intents), entries)
				return nil
			}
			n, err := store.ReplayForget(cliAuditContext(store))
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %d forgotten entries\n", n)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "execute Replay; without it this is a dry run")
	return cmd
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
				cfg.Memory.PrivateChannelExclusions, true, cfg.Memory.BudgetTokens)
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

func cliAuditContext(store *memstore.Store) context.Context {
	actor := "cli@local"
	if ledger := store.ForgetLedgerRef(); ledger != nil && ledger.UUID() != "" {
		actor = "cli@" + ledger.UUID()
	}
	return memstore.WithAudit(context.Background(), actor, "")
}

func openStore() (*memstore.Store, *config.Config, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	store, err := memstore.OpenForWorkspace(cfg.Agents.Defaults.Workspace, memstore.Config{
		Enabled:                  cfg.Memory.Enabled,
		DBPath:                   cfg.Memory.DBPath,
		BudgetTokens:             cfg.Memory.BudgetTokens,
		AutoExtract:              cfg.Memory.AutoExtract,
		PrivateChannelExclusions: cfg.Memory.PrivateChannelExclusions,
		InjectFictional:          cfg.Memory.InjectFictional,
		ReviewRequired:           cfg.Memory.ReviewRequired,
		EpisodicRetentionDays:    cfg.Memory.EpisodicRetentionDays,
	})
	if err != nil {
		return nil, nil, err
	}
	return store, cfg, nil
}

// newCleanupCommand drains queued forget-cleanup jobs into the session JSONL
// backend. It deletes only messages whose content exactly matches the HMAC
// recorded at forget time and reports anything it cannot verify as partial.
func newCleanupCommand() *cobra.Command {
	var maxJobs int
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Drain queued forget-cleanup jobs against JSONL and Seahorse",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if running := pid.ReadPidFileWithCheck(config.GetHome()); running != nil {
				return fmt.Errorf(
					"gateway is running (PID %d); stop it before running offline cleanup",
					running.PID)
			}
			store, cfg, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			ledger := store.ForgetLedgerRef()
			if ledger == nil {
				return fmt.Errorf("forget ledger unavailable; nothing cleaned")
			}
			sessionsDir := filepath.Join(cfg.Agents.Defaults.Workspace, "sessions")
			sessionStore, err := sessionmem.NewJSONLStore(sessionsDir)
			if err != nil {
				return err
			}
			defer sessionStore.Close()
			digest := func(text string) string {
				value, _, _ := ledger.Digest(text)
				return value
			}
			cleaners := cliCleanerGroup{sessionmem.NewLocatorCleaner(sessionStore, digest)}
			seahorseEngine, seahorseErr := seahorse.NewEngine(seahorse.Config{
				DBPath: filepath.Join(cfg.Agents.Defaults.Workspace, "sessions", "seahorse.db"),
			}, nil)
			if seahorseErr == nil && seahorseEngine != nil {
				defer seahorseEngine.Close()
				cleaners = append(cleaners, seahorse.NewLocatorCleaner(seahorseEngine, digest))
			}
			outcome, err := store.RunCleanupJobs(context.Background(), cleaners, maxJobs)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(outcome); err != nil {
				return err
			}
			if outcome.Unavailable > 0 || outcome.OutcomeUnknown > 0 {
				return fmt.Errorf("cleanup incomplete: unavailable=%d outcome_unknown=%d",
					outcome.Unavailable, outcome.OutcomeUnknown)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&maxJobs, "max-jobs", 20, "maximum cleanup jobs to process")
	return cmd
}

func newLedgerVerifyCommand() *cobra.Command {
	var backup string
	cmd := &cobra.Command{
		Use:   "ledger-verify",
		Short: "Verify a forget-ledger backup matches the active ledger identity",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(backup) == "" {
				return fmt.Errorf("--backup is required")
			}
			store, _, err := openStore()
			if err != nil {
				return err
			}
			defer store.Close()
			result, verifyErr := store.VerifyLedgerBackup(backup)
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return err
			}
			return verifyErr
		},
	}
	cmd.Flags().StringVar(&backup, "backup", "", "path to a forget-ledger.db backup")
	return cmd
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
				"pico", cfg.Memory.PrivateChannelExclusions)
			retrievalBudget := cfg.Memory.BudgetTokens - usage.TotalTokens
			if retrievalBudget < 200 {
				retrievalBudget = 200
			}
			retrieval := store.BuildRetrievalContext(ctx, "本质定调", "pico", retrievalBudget,
				cfg.Memory.InjectFictional, cfg.Memory.PrivateChannelExclusions)
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
			entries, err := store.ExportEntries(context.Background(), memstore.SearchOptions{})
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
				"pico", nil)
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
