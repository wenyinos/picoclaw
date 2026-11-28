package agent

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	sessionmem "github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/session"
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
		total.Receipts = append(total.Receipts, outcome.Receipts...)
	}
	return total, nil
}

// runRecoveryPreflight completes safe ledger reconciliation before the agent
// starts accepting turns. A failed or incomplete preflight does not stop normal
// chat, but MemoryService and context assembly stay fail-closed.
func (al *AgentLoop) runRecoveryPreflight(ctx context.Context) {
	if al == nil || al.cfg == nil {
		return
	}
	workspace := strings.TrimSpace(al.cfg.Agents.Defaults.Workspace)
	if workspace == "" {
		return
	}
	store, err := memstore.OpenForWorkspace(workspace, memstoreConfigFrom(al.cfg.Memory))
	if err != nil {
		logger.WarnCF("memory", "recovery preflight skipped: store unavailable", map[string]any{
			"error": err.Error(),
		})
		return
	}
	defer store.Close()
	preflightCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	status, err := store.RecoveryPreflight(preflightCtx)
	if err != nil {
		logger.WarnCF("memory", "recovery preflight failed", map[string]any{"error": err.Error()})
		return
	}
	fields := map[string]any{
		"ready":           status.Ready,
		"pending_intents": status.PendingIntents,
		"blocked_intents": status.BlockedIntents,
		"blocked_cleanup": status.BlockedCleanup,
		"inert_cleanup":   status.InertCleanup,
		"orphan_entries":  status.OrphanEntries,
		"domain_registry": status.DomainRegistryOK,
	}
	if status.Ready {
		logger.InfoCF("memory", "recovery preflight ready", fields)
	} else {
		fields["issues"] = status.Issues
		logger.WarnCF("memory", "recovery preflight requires owner action", fields)
	}
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
	digest := func(text string) string {
		value, _, _ := ledger.Digest(text)
		return value
	}
	cleaners := compositeCleaner{}
	seenStores := map[*sessionmem.JSONLStore]bool{}
	if al.registry != nil {
		for _, agentID := range al.registry.ListAgentIDs() {
			agent, ok := al.registry.GetAgent(agentID)
			if !ok || agent == nil || agent.Sessions == nil {
				continue
			}
			backend, ok := agent.Sessions.(*session.JSONLBackend)
			if !ok {
				continue
			}
			active := backend.JSONLStore()
			if active == nil || seenStores[active] {
				continue
			}
			seenStores[active] = true
			cleaners = append(cleaners, sessionmem.NewLocatorCleaner(active, digest))
		}
	}
	var fallback *sessionmem.JSONLStore
	if len(cleaners) == 0 {
		fallback, err = sessionmem.NewJSONLStore(filepath.Join(workspace, "sessions"))
		if err != nil {
			logger.WarnCF("memory", "cleanup skipped: session store unavailable", map[string]any{
				"error": err.Error(),
			})
			return
		}
		defer fallback.Close()
		cleaners = append(cleaners, sessionmem.NewLocatorCleaner(fallback, digest))
	}
	cleaners = append(cleaners, al.seahorseCleanersForCleanup(digest)...)

	run := func() {
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		reconcile, err := store.ReconcilePendingIntents(runCtx, false)
		if err != nil {
			logger.WarnCF("memory", "pending ledger reconciliation failed", map[string]any{
				"error": err.Error(),
			})
		} else if reconcile.Scanned > 0 {
			logger.InfoCF("memory", "pending ledger reconciliation finished", map[string]any{
				"scanned":             reconcile.Scanned,
				"applied_bookkeeping": reconcile.AppliedBookkeeping,
				"replayed":            reconcile.Replayed,
				"blocked":             reconcile.Blocked,
				"skipped_unapproved":  reconcile.SkippedUnapproved,
			})
		}
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
		al.runExtractionJobs(runCtx)
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
