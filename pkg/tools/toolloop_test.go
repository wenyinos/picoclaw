package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type toolloopCountingProvider struct {
	calls int
}

func (p *toolloopCountingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{Content: "ok", FinishReason: "stop"}, nil
}

func (p *toolloopCountingProvider) GetDefaultModel() string { return "test-model" }

func TestRunToolLoopGuardBlocksProviderCall(t *testing.T) {
	provider := &toolloopCountingProvider{}
	guardErr := errors.New("policy denied")
	ctx := WithLLMCallGuard(context.Background(), func(context.Context, string) error {
		return guardErr
	})

	result, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		MaxIterations: 1,
	}, []providers.Message{{Role: "user", Content: "do work"}}, "telegram", "chat-1")
	if !errors.Is(err, guardErr) {
		t.Fatalf("RunToolLoop error = %v, want guard error", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil after denied call", result)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
}

func TestRunToolLoopGuardAllowsProviderCall(t *testing.T) {
	provider := &toolloopCountingProvider{}
	ctx := WithLLMCallGuard(context.Background(), func(context.Context, string) error {
		return nil
	})

	result, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      provider,
		Model:         "test-model",
		MaxIterations: 1,
	}, []providers.Message{{Role: "user", Content: "do work"}}, "telegram", "chat-1")
	if err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	if result == nil || result.Content != "ok" {
		t.Fatalf("result = %+v, want content ok", result)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}
