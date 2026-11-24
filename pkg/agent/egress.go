package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
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
// The legacy path keeps the pre-existing gate (memory enabled and non-zero
// budget). Session-derived content uses checkMemoryEgressFor with
// sessionDerived=true, which cannot be disabled by turning memory off or
// setting its budget to zero.
func (p *Pipeline) checkMemoryEgress(providerName, model string) error {
	return p.checkMemoryEgressFor(providerName, model, false)
}

func (p *Pipeline) checkMemoryEgressFor(providerName, model string, sessionDerived bool) error {
	if p == nil {
		return nil
	}
	return checkMemoryEgressFor(p.Cfg, providerName, model, sessionDerived)
}

func checkMemoryEgressFor(cfg *config.Config, providerName, model string, sessionDerived bool) error {
	if cfg == nil {
		return nil
	}
	if !sessionDerived && (!cfg.Memory.Enabled || cfg.Memory.BudgetTokens <= 0) {
		return nil
	}
	allowed := cfg.Memory.AllowedChatRecipients
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

// agentPrimaryRecipient returns the configured provider/model identity used by
// the agent's primary candidate. Background paths that do not carry a
// FallbackCandidate still need the same recipient identity for policy checks.
func agentPrimaryRecipient(agent *AgentInstance) (providerName, model string) {
	if agent == nil {
		return "", ""
	}
	model = strings.TrimSpace(agent.Model)
	if len(agent.Candidates) > 0 {
		candidate := agent.Candidates[0]
		providerName = strings.TrimSpace(candidate.Provider)
		if candidateModel := strings.TrimSpace(candidate.Model); candidateModel != "" {
			model = candidateModel
		}
	}
	return providerName, model
}

// messagesCarryMemoryContext reports whether the assembled prompt includes
// long-term memory or conversation-summary blocks. Those calls must remain
// subject to recipient policy even when memory.enabled is false or the memory
// budget is zero.
func messagesCarryMemoryContext(messages []providers.Message) bool {
	for _, msg := range messages {
		for _, part := range msg.SystemParts {
			switch part.PromptSlot {
			case string(PromptSlotMemory), string(PromptSlotSummary):
				return true
			}
		}
	}
	return false
}

// checkActiveCandidateEgress is the guard used by paths that only know the
// selected candidate list (streaming, retries).
func (p *Pipeline) checkActiveCandidateEgress(exec *turnExecution) error {
	return p.checkActiveCandidateEgressFor(exec, false)
}

func (p *Pipeline) checkActiveCandidateEgressFor(exec *turnExecution, sessionDerived bool) error {
	if exec == nil || len(exec.activeCandidates) == 0 {
		return nil
	}
	primary := exec.activeCandidates[0]
	return p.checkMemoryEgressFor(primary.Provider, primary.Model, sessionDerived)
}
