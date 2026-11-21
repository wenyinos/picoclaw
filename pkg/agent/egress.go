package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// ErrMemoryEgressDenied is a non-retryable policy refusal. A provider that is
// not authorized to receive long-term memory must not be called, and a
// fallback chain must not silently route the same request to another
// unlisted provider (FR-28/FR-30, §10.7).
var ErrMemoryEgressDenied = errors.New("memory egress denied")

// checkMemoryEgress enforces memory.allowed_chat_recipients at the actual
// send point. An empty list is compatibility mode for existing deployments;
// once the owner lists recipients, every model request that can carry memory
// must match one of them.
//
// The check is intentionally conservative: it runs when memory is enabled and
// has a non-zero budget, not only when this exact turn happened to retrieve a
// row. A configuration change therefore takes effect on the next request.
func (p *Pipeline) checkMemoryEgress(providerName, model string) error {
	if p == nil || p.Cfg == nil || !p.Cfg.Memory.Enabled {
		return nil
	}
	if p.Cfg.Memory.BudgetTokens <= 0 {
		return nil
	}
	allowed := p.Cfg.Memory.AllowedChatRecipients
	if len(allowed) == 0 {
		return nil
	}
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	for _, raw := range allowed {
		a := strings.TrimSpace(raw)
		if a == "" {
			continue
		}
		if strings.EqualFold(a, providerName) ||
			(model != "" && strings.EqualFold(a, providerName+"/"+model)) {
			return nil
		}
	}
	logger.WarnCF("agent", "memory egress denied by recipient allow-list", map[string]any{
		"provider": providerName,
		"model":    model,
	})
	return fmt.Errorf("%w: provider %q model %q is not in memory.allowed_chat_recipients",
		ErrMemoryEgressDenied, providerName, model)
}

// checkActiveCandidateEgress is the guard used by paths that only know the
// selected candidate list (streaming, retries).
func (p *Pipeline) checkActiveCandidateEgress(exec *turnExecution) error {
	if exec == nil || len(exec.activeCandidates) == 0 {
		return nil
	}
	primary := exec.activeCandidates[0]
	return p.checkMemoryEgress(primary.Provider, primary.Model)
}
