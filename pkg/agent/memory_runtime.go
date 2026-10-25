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
		Enabled:               cfg.Enabled,
		DBPath:                cfg.DBPath,
		BudgetTokens:          cfg.BudgetTokens,
		AutoExtract:           cfg.AutoExtract,
		ExtractModel:          cfg.ExtractModel,
		PrivateChannels:       append([]string(nil), cfg.PrivateChannels...),
		InjectFictional:       cfg.InjectFictional,
		ReviewRequired:        cfg.ReviewRequired,
		EpisodicRetentionDays: cfg.EpisodicRetentionDays,
	}
}

// extractMemoryAsync applies conservative rule-based extraction after a turn.
// It never blocks the response path; database failures only log.
func (al *AgentLoop) extractMemoryAsync(
	workspace string,
	cfg config.MemoryConfig,
	userText, assistantText, sessionKey string,
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		results, err := store.ExtractTurn(ctx, userText, assistantText, sessionKey, cfg.ReviewRequired)
		if err != nil {
			logger.DebugCF("memory", "extraction failed", map[string]any{"error": err.Error()})
			return
		}
		if len(results) > 0 {
			logger.InfoCF("memory", "memories extracted", map[string]any{
				"count":   len(results),
				"session": sessionKey,
			})
		}
	}()
}
