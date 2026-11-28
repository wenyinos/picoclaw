package agent

import (
	"context"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memstore"
)

func memstoreConfigFrom(cfg config.MemoryConfig) memstore.Config {
	return memstore.Config{
		Enabled:                  cfg.Enabled,
		DBPath:                   cfg.DBPath,
		BudgetTokens:             cfg.BudgetTokens,
		AutoExtract:              cfg.AutoExtract,
		ExtractModel:             cfg.ExtractModel,
		PrivateChannelExclusions: append([]string(nil), cfg.PrivateChannelExclusions...),
		InjectFictional:          cfg.InjectFictional,
		ReviewRequired:           cfg.ReviewRequired,
		EpisodicRetentionDays:    cfg.EpisodicRetentionDays,
	}
}

// sharedChatKind reports whether this turn arrived from a group or public
// chat. Shared conversations must never feed the personal long-term profile:
// the text belongs to the group, not to the owner (0.7.3). A chat context with
// a missing or unknown ChatType fails closed as shared.
func sharedChatKind(ts *turnState) bool {
	if ts == nil {
		return false
	}
	req := ts.opts.Dispatch
	if req.InboundContext == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(req.InboundContext.ChatType)) {
	case "group", "channel", "public":
		return true
	case "direct", "private":
		return false
	}
	return strings.TrimSpace(req.InboundContext.Channel) != "" ||
		strings.TrimSpace(req.InboundContext.ChatID) != ""
}

// shouldAutoExtractMemory reports whether the turn text has trusted private
// provenance. Shared/unknown chats and session-derived async follow-ups are
// excluded from automatic learning.
func shouldAutoExtractMemory(ts *turnState) bool {
	if ts == nil {
		return false
	}
	return ts.depth == 0 && ts.parentTurnState == nil &&
		!sharedChatKind(ts) && !isAsyncFollowUpTurn(ts)
}

// shouldAutoExtractMemoryForConfig also honors the private-channel exclusion
// policy, so a channel excluded from private memory does not feed automatic
// long-term extraction even if its adapter reports a direct chat.
func shouldAutoExtractMemoryForConfig(ts *turnState, cfg config.MemoryConfig) bool {
	if !shouldAutoExtractMemory(ts) {
		return false
	}
	channel := strings.TrimSpace(ts.channel)
	if channel == "" {
		channel = strings.TrimSpace(ts.opts.Dispatch.Channel())
	}
	for _, excluded := range cfg.PrivateChannelExclusions {
		if strings.EqualFold(strings.TrimSpace(excluded), channel) {
			return false
		}
	}
	return true
}

// extractMemoryAsync persists a durable extraction reference after a turn. The
// message text stays in the session backend; a keyed digest identifies it when
// the worker runs. If the process dies, the job remains visible for the
// periodic recovery worker.
func (al *AgentLoop) extractMemoryAsync(
	workspace string,
	cfg config.MemoryConfig,
	source memstore.ExtractSource,
	userText string,
) {
	if !cfg.Enabled || !cfg.AutoExtract || strings.TrimSpace(userText) == "" {
		return
	}
	store, err := memstore.OpenForWorkspace(workspace, memstoreConfigFrom(cfg))
	if err != nil {
		logger.DebugCF("memory", "extraction skipped: store unavailable", map[string]any{
			"workspace": workspace,
			"error":     err.Error(),
		})
		return
	}
	materialize := func(ctx context.Context) {
		jobID, err := store.EnqueueExtractionJob(ctx, source, userText)
		if err != nil {
			logger.DebugCF("memory", "extraction enqueue failed", map[string]any{"error": err.Error()})
			return
		}
		if jobID != "" {
			logger.DebugCF("memory", "extraction job queued", map[string]any{"job_id": jobID})
			go al.runExtractionJobs(context.Background())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	materialize(ctx)
	cancel()
}
