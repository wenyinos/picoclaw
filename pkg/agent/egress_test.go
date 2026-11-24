package agent

import (
	"context"
	"errors"
	"strings"
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

func TestMemoryEgressSessionDerivedIgnoresDisabledAndZeroBudget(t *testing.T) {
	for _, memoryEnabled := range []bool{true, false} {
		cfg := &config.Config{}
		cfg.Memory.Enabled = memoryEnabled
		cfg.Memory.BudgetTokens = 0
		cfg.Memory.AllowedChatRecipients = []string{"openai"}

		if err := checkMemoryEgressFor(cfg, "deepseek", "chat", true); !errors.Is(err, ErrMemoryEgressDenied) {
			t.Fatalf("enabled=%v zero budget: unlisted summary provider error = %v", memoryEnabled, err)
		}
		if err := checkMemoryEgressFor(cfg, "openai", "chat", true); err != nil {
			t.Fatalf("enabled=%v zero budget: listed summary provider rejected: %v", memoryEnabled, err)
		}
	}
}

func TestMessagesCarryMemoryContext(t *testing.T) {
	plain := []providers.Message{{Role: "user", Content: "hello"}}
	if messagesCarryMemoryContext(plain) {
		t.Fatal("plain chat must not require memory egress policy")
	}
	for _, slot := range []PromptSlot{PromptSlotMemory, PromptSlotSummary} {
		messages := []providers.Message{{
			Role: "system",
			SystemParts: []providers.ContentBlock{{
				Type:       "text",
				PromptSlot: string(slot),
			}},
		}}
		if !messagesCarryMemoryContext(messages) {
			t.Fatalf("slot %q must require memory egress policy", slot)
		}
	}
}

type egressCountingProvider struct {
	calls int
}

func (p *egressCountingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{Content: "ok", FinishReason: "stop"}, nil
}

func (p *egressCountingProvider) GetDefaultModel() string { return "test-model" }

func TestCallLLMPolicyDenialStopsBeforeProvider(t *testing.T) {
	provider := &egressCountingProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	cfg := al.GetConfig()
	cfg.Memory.Enabled = true
	cfg.Memory.BudgetTokens = 1200
	cfg.Memory.AllowedChatRecipients = []string{"openai/allowed-model"}
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "openai", Model: "denied-model"},
	}

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn: %v", err)
	}

	_, err = pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
	if !errors.Is(err, ErrMemoryEgressDenied) {
		t.Fatalf("CallLLM error = %v, want ErrMemoryEgressDenied", err)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestCallLLMPolicyDenialStopsFallbackChain(t *testing.T) {
	provider := &egressCountingProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	cfg := al.GetConfig()
	cfg.Memory.Enabled = true
	cfg.Memory.BudgetTokens = 1200
	cfg.Memory.AllowedChatRecipients = []string{"openai/allowed-model"}
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "openai", Model: "denied-model"},
		{Provider: "openai", Model: "allowed-model"},
	}
	al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker(), nil)

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn: %v", err)
	}

	_, err = pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
	if !errors.Is(err, ErrMemoryEgressDenied) {
		t.Fatalf("CallLLM error = %v, want ErrMemoryEgressDenied", err)
	}
	if provider.calls != 0 {
		t.Fatalf("fallback reached provider %d times, want 0", provider.calls)
	}
}

func TestLegacySummaryEgressDeniedUsesLocalFallback(t *testing.T) {
	provider := &egressCountingProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	cfg := al.GetConfig()
	cfg.Memory.Enabled = false
	cfg.Memory.BudgetTokens = 0
	cfg.Memory.AllowedChatRecipients = []string{"openai/allowed-model"}
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "openai", Model: "denied-model"},
	}

	manager := &legacyContextManager{al: al}
	summary, err := manager.summarizeBatch(
		context.Background(),
		agent,
		[]providers.Message{{Role: "user", Content: "private conversation text"}},
		"",
	)
	if err != nil {
		t.Fatalf("summarizeBatch: %v", err)
	}
	if summary == "" {
		t.Fatal("expected local fallback summary")
	}
	if provider.calls != 0 {
		t.Fatalf("summary reached provider %d times, want 0", provider.calls)
	}
}

func TestSideQuestionEgressDeniedBeforeProviderCall(t *testing.T) {
	provider := &egressCountingProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	cfg := al.GetConfig()
	cfg.Memory.Enabled = false
	cfg.Memory.BudgetTokens = 0
	cfg.Memory.AllowedChatRecipients = []string{"openai/allowed-model"}
	agent.Candidates = []providers.FallbackCandidate{
		{Provider: "openai", Model: "denied-model"},
	}
	al.providerFactory = func(*config.ModelConfig) (providers.LLMProvider, string, error) {
		return provider, "denied-model", nil
	}

	opts := &processOptions{
		SessionKey: "btw-session",
		Channel:    "telegram",
		ChatID:     "chat-1",
		InboundContext: &bus.InboundContext{
			Channel:  "telegram",
			ChatID:   "chat-1",
			ChatType: "private",
		},
	}
	_, err := al.askSideQuestion(context.Background(), agent, opts, "private question")
	if !errors.Is(err, ErrMemoryEgressDenied) {
		t.Fatalf("askSideQuestion error = %v, want ErrMemoryEgressDenied", err)
	}
	if provider.calls != 0 {
		t.Fatalf("/btw reached provider %d times, want 0", provider.calls)
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

func TestSharedChatKindFailsClosedForUnknownChatContext(t *testing.T) {
	for _, chatType := range []string{"", "unknown", "thread"} {
		ts := &turnState{opts: processOptions{Dispatch: DispatchRequest{
			InboundContext: &bus.InboundContext{
				Channel:  "telegram",
				ChatID:   "chat-1",
				ChatType: chatType,
			},
		}}}
		if !sharedChatKind(ts) {
			t.Fatalf("chat type %q with channel context must fail closed as shared", chatType)
		}
	}

	// No chat context keeps CLI/maintenance behavior.
	if sharedChatKind(mkWithChannel("", "")) {
		t.Fatal("empty chat context must not be treated as shared")
	}
}

func mkWithChannel(channel, chatID string) *turnState {
	return &turnState{opts: processOptions{Dispatch: DispatchRequest{
		InboundContext: &bus.InboundContext{Channel: channel, ChatID: chatID},
	}}}
}

func TestFormatMessagesForLogRedactsMemoryBodies(t *testing.T) {
	const sentinel = "SENTINEL-private-memory-body-do-not-log"
	logged := formatMessagesForLog([]providers.Message{
		{Role: "system", Content: sentinel},
		{Role: "user", Content: sentinel},
	})
	if strings.Contains(logged, sentinel) {
		t.Fatalf("DEBUG log formatter leaked message body: %s", logged)
	}
	if !strings.Contains(logged, "sha256=") {
		t.Fatalf("expected fingerprint instead of body, got: %s", logged)
	}
}

func TestChatKindFromTypeFailsClosedForUnknownChatContext(t *testing.T) {
	for _, chatType := range []string{"", "unknown", "thread"} {
		kind := chatKindFromType(&bus.InboundContext{
			Channel:  "telegram",
			ChatID:   "chat-1",
			ChatType: chatType,
		})
		if kind != "public" {
			t.Fatalf("chat type %q => %q, want public fail-closed", chatType, kind)
		}
	}
	if kind := chatKindFromType(&bus.InboundContext{}); kind != "" {
		t.Fatalf("empty context => %q, want empty maintenance kind", kind)
	}
	if kind := chatKindFromType(&bus.InboundContext{ChatType: "direct"}); kind != "private" {
		t.Fatalf("direct => %q, want private", kind)
	}
}

func TestShouldAutoExtractMemory(t *testing.T) {
	direct := &turnState{opts: processOptions{Dispatch: DispatchRequest{
		InboundContext: &bus.InboundContext{Channel: "telegram", ChatID: "c1", ChatType: "direct"},
	}}}
	if !shouldAutoExtractMemory(direct) {
		t.Fatal("trusted direct chat should allow auto extraction")
	}
	group := &turnState{opts: processOptions{Dispatch: DispatchRequest{
		InboundContext: &bus.InboundContext{Channel: "telegram", ChatID: "c1", ChatType: "group"},
	}}}
	if shouldAutoExtractMemory(group) {
		t.Fatal("group chat must not auto-extract")
	}
	unknown := &turnState{opts: processOptions{Dispatch: DispatchRequest{
		InboundContext: &bus.InboundContext{Channel: "telegram", ChatID: "c1", ChatType: "unknown"},
	}}}
	if shouldAutoExtractMemory(unknown) {
		t.Fatal("unknown chat kind must not auto-extract")
	}
	async := &turnState{opts: processOptions{Dispatch: DispatchRequest{
		InboundContext: &bus.InboundContext{
			Channel:  "telegram",
			ChatID:   "c1",
			ChatType: "direct",
			SenderID: asyncFollowUpSenderPrefix + "web_search",
		},
	}}}
	if shouldAutoExtractMemory(async) {
		t.Fatal("async follow-up must not auto-extract")
	}
}

func TestAsyncFollowUpInboundInheritsOrigin(t *testing.T) {
	ts := &turnState{
		channel:    "telegram",
		chatID:     "chat-42",
		sessionKey: "sk_v1_test",
		opts: processOptions{Dispatch: DispatchRequest{
			InboundContext: &bus.InboundContext{
				Channel:  "telegram",
				ChatID:   "chat-42",
				ChatType: "group",
			},
		}},
	}
	msg := asyncFollowUpInboundMessage(ts, "web_search", "background result")
	if msg.Context.Channel != "telegram" || msg.Context.ChatID != "chat-42" {
		t.Fatalf("follow-up channel/chat = %q/%q, want telegram/chat-42", msg.Context.Channel, msg.Context.ChatID)
	}
	if msg.Channel != "telegram" || msg.ChatID != "chat-42" {
		t.Fatalf("follow-up mirror channel/chat = %q/%q, want telegram/chat-42", msg.Channel, msg.ChatID)
	}
	if msg.Context.ChatType != "group" {
		t.Fatalf("follow-up chat type = %q, want group", msg.Context.ChatType)
	}
	if msg.SessionKey != "sk_v1_test" {
		t.Fatalf("follow-up session key = %q, want sk_v1_test", msg.SessionKey)
	}
	if !strings.HasPrefix(msg.Context.SenderID, asyncFollowUpSenderPrefix) {
		t.Fatalf("follow-up sender = %q, want async prefix", msg.Context.SenderID)
	}
	followUpTurn := &turnState{opts: processOptions{Dispatch: DispatchRequest{
		InboundContext: &msg.Context,
	}}}
	if !isAsyncFollowUpTurn(followUpTurn) {
		t.Fatal("async follow-up turn must be recognized as not-safe-for-extraction")
	}
}
