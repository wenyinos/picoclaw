package seahorse

import (
	"context"
	"errors"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func TestGrepSearchSummaries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:grep-tool")

	s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: conv.ConversationID,
		Kind:           SummaryKindLeaf,
		Depth:          0,
		Content:        "database connection pool configuration",
		TokenCount:     50,
	})

	re := &RetrievalEngine{store: s}
	results, err := re.Grep(ctx, GrepInput{
		Pattern:        "database",
		ConversationID: conv.ConversationID,
	})
	if err != nil {
		t.Fatalf("Grep: %v", err)
	}
	if len(results.Summaries) == 0 {
		t.Error("expected at least 1 summary result")
	}
}

func TestGrepSearchMessages(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:grep-msg")

	s.AddMessage(ctx, conv.ConversationID, "user", "find this message about testing", 5)
	s.AddMessage(ctx, conv.ConversationID, "user", "unrelated content", 3)

	re := &RetrievalEngine{store: s}
	results, err := re.Grep(ctx, GrepInput{
		Pattern:        "testing",
		ConversationID: conv.ConversationID,
	})
	if err != nil {
		t.Fatalf("Grep messages: %v", err)
	}
	if len(results.Messages) == 0 {
		t.Error("expected at least 1 message result")
	}
}

func TestGrepMissingPattern(t *testing.T) {
	s := openTestStore(t)
	re := &RetrievalEngine{store: s}
	_, err := re.Grep(context.Background(), GrepInput{})
	if err == nil {
		t.Error("expected error for missing pattern")
	}
}

func TestGrepToolSupportsAllConversations(t *testing.T) {
	s := openTestStore(t)
	tool := NewGrepTool(&RetrievalEngine{store: s})
	params := tool.Parameters()
	props := params["properties"].(map[string]any)

	// GrepTool should accept all_conversations parameter
	if _, ok := props["all_conversations"]; !ok {
		t.Error("Parameters missing 'all_conversations' field")
	}
}

func seahorseToolContext(agentID, sessionKey string) context.Context {
	return toolshared.WithToolSessionContext(context.Background(), agentID, sessionKey, nil)
}

func fixedConversationResolver(conversationID int64) ConversationResolver {
	return func(context.Context, string, string) (int64, error) {
		return conversationID, nil
	}
}

func TestGrepToolRejectsMissingIdentity(t *testing.T) {
	s := openTestStore(t)
	tool := NewGrepTool(&RetrievalEngine{store: s}, fixedConversationResolver(1))

	result := tool.Execute(context.Background(), map[string]any{"pattern": "secret"})
	if !result.IsError {
		t.Fatal("expected error when tool session identity is missing")
	}
}

func TestGrepToolRejectsAllConversations(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "agent:main:test:grep-all")
	tool := NewGrepTool(&RetrievalEngine{store: s}, fixedConversationResolver(conv.ConversationID))

	result := tool.Execute(
		seahorseToolContext("main", conv.SessionKey),
		map[string]any{"pattern": "anything", "all_conversations": true},
	)
	if !result.IsError {
		t.Fatal("expected error for model all_conversations=true")
	}
	if !strings.Contains(result.ForLLM, "all_conversations") {
		t.Fatalf("error should mention all_conversations, got %q", result.ForLLM)
	}
}

func TestGrepToolDefaultsToTrustedConversation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	trusted, _ := s.GetOrCreateConversation(ctx, "agent:main:test:trusted")
	other, _ := s.GetOrCreateConversation(ctx, "agent:main:test:other")

	s.AddMessage(ctx, trusted.ConversationID, "user", "shared-keyword trusted", 5)
	s.AddMessage(ctx, other.ConversationID, "user", "shared-keyword other", 5)
	s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: trusted.ConversationID,
		Kind:           SummaryKindLeaf,
		Content:        "shared-keyword trusted summary",
		TokenCount:     10,
	})
	s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: other.ConversationID,
		Kind:           SummaryKindLeaf,
		Content:        "shared-keyword other summary",
		TokenCount:     10,
	})

	tool := NewGrepTool(&RetrievalEngine{store: s}, fixedConversationResolver(trusted.ConversationID))
	result := tool.Execute(
		seahorseToolContext("main", trusted.SessionKey),
		map[string]any{"pattern": "%shared-keyword%"},
	)
	if result.IsError {
		t.Fatalf("grep failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "trusted") {
		t.Fatalf("trusted conversation result missing: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "shared-keyword other") {
		t.Fatalf("grep leaked another conversation: %s", result.ForLLM)
	}
}

func TestGrepToolFailsClosedWhenAuthorizationFails(t *testing.T) {
	s := openTestStore(t)
	tool := NewGrepTool(&RetrievalEngine{store: s}, func(context.Context, string, string) (int64, error) {
		return 0, errors.New("not authorized")
	})
	result := tool.Execute(
		seahorseToolContext("main", "agent:main:test"),
		map[string]any{"pattern": "secret"},
	)
	if !result.IsError {
		t.Fatal("expected authorization failure")
	}
}
