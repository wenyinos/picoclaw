package agent

import (
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMemoryEgressAllowList(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.BudgetTokens = 1200
	cfg.Memory.AllowedChatRecipients = []string{"openai/gpt-4o", "local"}
	p := &Pipeline{Cfg: cfg}

	if err := p.checkMemoryEgress("openai", "gpt-4o"); err != nil {
		t.Fatalf("listed provider rejected: %v", err)
	}
	if err := p.checkMemoryEgress("local", "any-model"); err != nil {
		t.Fatalf("provider-only entry rejected: %v", err)
	}
	err := p.checkMemoryEgress("deepseek", "chat")
	if !errors.Is(err, ErrMemoryEgressDenied) {
		t.Fatalf("unlisted provider error = %v, want ErrMemoryEgressDenied", err)
	}
}

func TestMemoryEgressCompatibilityWhenUnconfigured(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.BudgetTokens = 1200
	p := &Pipeline{Cfg: cfg}
	if err := p.checkMemoryEgress("anything", "anything"); err != nil {
		t.Fatalf("empty allow-list must stay compatible: %v", err)
	}
}

func TestMemoryEgressDisabledMemory(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = false
	cfg.Memory.BudgetTokens = 1200
	cfg.Memory.AllowedChatRecipients = []string{"openai"}
	p := &Pipeline{Cfg: cfg}
	if err := p.checkMemoryEgress("deepseek", "chat"); err != nil {
		t.Fatalf("disabled memory must not gate providers: %v", err)
	}
}

func TestMemoryEgressZeroBudget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.BudgetTokens = 0
	cfg.Memory.AllowedChatRecipients = []string{"openai"}
	p := &Pipeline{Cfg: cfg}
	if err := p.checkMemoryEgress("deepseek", "chat"); err != nil {
		t.Fatalf("zero budget means no memory is injected: %v", err)
	}
}
