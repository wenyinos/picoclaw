package agent

import (
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
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

func TestActiveCandidateEgressChecksPrimary(t *testing.T) {
	cfg := &config.Config{}
	cfg.Memory.Enabled = true
	cfg.Memory.BudgetTokens = 1200
	cfg.Memory.AllowedChatRecipients = []string{"openai/gpt-4o"}
	p := &Pipeline{Cfg: cfg}
	exec := &turnExecution{activeCandidates: []providers.FallbackCandidate{
		{Provider: "deepseek", Model: "chat"},
	}}
	if err := p.checkActiveCandidateEgress(exec); !errors.Is(err, ErrMemoryEgressDenied) {
		t.Fatalf("single-provider candidate was not gated: %v", err)
	}
	exec.activeCandidates[0] = providers.FallbackCandidate{Provider: "openai", Model: "gpt-4o"}
	if err := p.checkActiveCandidateEgress(exec); err != nil {
		t.Fatalf("listed candidate rejected: %v", err)
	}
}

func TestSharedChatKind(t *testing.T) {
	mk := func(chatType string) *turnState {
		return &turnState{opts: processOptions{Dispatch: DispatchRequest{
			InboundContext: &bus.InboundContext{ChatType: chatType},
		}}}
	}
	if sharedChatKind(nil) {
		t.Fatal("nil turn state must not count as shared")
	}
	if sharedChatKind(&turnState{}) {
		t.Fatal("missing inbound context must not count as shared")
	}
	if sharedChatKind(mk("direct")) {
		t.Fatal("direct chat must not count as shared")
	}
	for _, kind := range []string{"group", "channel", "public", "Group"} {
		if !sharedChatKind(mk(kind)) {
			t.Fatalf("chat type %q must count as shared", kind)
		}
	}
}
