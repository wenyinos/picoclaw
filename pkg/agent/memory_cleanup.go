package agent

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	sessionmem "github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

type compositeCleaner []memstore.MessageCleaner

func (c compositeCleaner) CleanLocators(ctx context.Context, locators []memstore.SourceLocator) (memstore.CleanupOutcome, error) {
	total := memstore.CleanupOutcome{}
	for _, cleaner := range c {
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

// runForgetCleanup drains accepted forget obligations at gateway start and on
// a slow ticker. Forgetting is asynchronous by design: the main database is
// already clean, while JSONL copies are only removed when their content can be
// verified against the ledger HMAC.
func (al *AgentLoop) runForgetCleanup(ctx context.Context) {
	if al == nil || al.cfg == nil {
		return
	}
	workspace := strings.TrimSpace(al.cfg.Agents.Defaults.Workspace)
	if workspace == "" {
		return
	}
	store, err := memstore.OpenForWorkspace(workspace, memstoreConfigFrom(al.cfg.Memory))
	if err != nil {
		logger.DebugCF("memory", "cleanup skipped: store unavailable", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if store.PendingMigration() {
		return
	}
	ledger := store.ForgetLedgerRef()
	if ledger == nil {
		logger.WarnCF("memory", "cleanup skipped: forget ledger unavailable", nil)
		return
	}
	jsonl, err := sessionmem.NewJSONLStore(filepath.Join(workspace, "sessions"))
	if err != nil {
		logger.WarnCF("memory", "cleanup skipped: session store unavailable", map[string]any{
			"error": err.Error(),
		})
		return
	}
	defer jsonl.Close()
	digest := func(text string) string {
		value, _, _ := ledger.Digest(text)
		return value
	}
	cleaners := compositeCleaner{sessionmem.NewLocatorCleaner(jsonl, digest)}
	al.mu.RLock()
	cm := al.contextManager
	al.mu.RUnlock()
	if mgr, ok := cm.(*seahorseContextManager); ok && mgr != nil && mgr.engine != nil {
		cleaners = append(cleaners, seahorse.NewLocatorCleaner(mgr.engine, digest))
	}

	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		outcome, err := store.RunCleanupJobs(runCtx, cleaners, 20)
		if err != nil {
			logger.WarnCF("memory", "forget cleanup failed", map[string]any{
				"error": err.Error(),
			})
			return
		}
		if outcome.Removed > 0 || outcome.Unavailable > 0 || len(outcome.Details) > 0 {
			logger.InfoCF("memory", "forget cleanup finished", map[string]any{
				"removed":     outcome.Removed,
				"unavailable": outcome.Unavailable,
				"sessions":    outcome.Sessions,
				"details":     len(outcome.Details),
			})
		}
	}

	run()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
