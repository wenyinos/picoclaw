//go:build !mipsle && !netbsd && !(freebsd && arm)

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
	"github.com/sipeed/picoclaw/pkg/seahorse"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tokenizer"
)

type seahorseSessionKeyCtxKey struct{}

func withSeahorseSessionKey(ctx context.Context, sessionKey string) context.Context {
	if strings.TrimSpace(sessionKey) == "" {
		return ctx
	}
	return context.WithValue(ctx, seahorseSessionKeyCtxKey{}, sessionKey)
}

func seahorseSessionKeyFromContext(ctx context.Context) string {
	value, _ := ctx.Value(seahorseSessionKeyCtxKey{}).(string)
	return value
}

// seahorseContextManager adapts seahorse.Engine to agent.ContextManager.
type seahorseContextManager struct {
	engine   *seahorse.Engine
	sessions session.SessionStore // for startup bootstrap
	al       *AgentLoop           // for resolving the agent that owns a session
}

// newSeahorseContextManager creates a seahorse-backed ContextManager.
func newSeahorseContextManager(_ json.RawMessage, al *AgentLoop) (ContextManager, error) {
	if al == nil {
		return nil, fmt.Errorf("seahorse: AgentLoop is required")
	}

	// Resolve workspace for DB path
	// DB stores session data, so it goes in sessions/ directory
	agent := al.registry.GetDefaultAgent()
	dbPath := agent.Workspace + "/sessions/seahorse.db"

	// Create CompleteFn from provider
	completeFn := providerToCompleteFn(al)

	// Create engine
	engine, err := seahorse.NewEngine(seahorse.Config{
		DBPath: dbPath,
	}, completeFn)
	if err != nil {
		return nil, fmt.Errorf("seahorse: create engine: %w", err)
	}

	mgr := &seahorseContextManager{
		engine:   engine,
		sessions: agent.Sessions,
		al:       al,
	}

	// Register seahorse tools with the agent's tool registry
	retrieval := mgr.engine.GetRetrieval()
	al.RegisterTool(seahorse.NewGrepTool(retrieval, mgr.resolveTrustedConversation))
	al.RegisterTool(seahorse.NewExpandTool(retrieval, mgr.resolveTrustedConversation))

	// Bootstrap all existing sessions at startup
	if agent.Sessions != nil {
		ctx := context.Background()
		for _, sessionKey := range agent.Sessions.ListSessions() {
			mgr.bootstrapSession(ctx, sessionKey)
		}
	}

	return mgr, nil
}

// providerToCompleteFn wraps the owning agent's provider as a
// seahorse.CompleteFn. The provider is resolved per call from the trusted
// session context so a background summary never reuses another agent's (or a
// stale default agent's) recipient identity.
func providerToCompleteFn(al *AgentLoop) seahorse.CompleteFn {
	return func(ctx context.Context, prompt string, opts seahorse.CompleteOptions) (string, error) {
		agent := al.seahorseSummaryAgent(ctx)
		if agent == nil || agent.Provider == nil {
			return "", fmt.Errorf("seahorse: summary provider is unavailable")
		}
		providerName, model := agentPrimaryRecipient(agent)
		if err := checkMemoryEgressFor(al.GetConfig(), providerName, model, true); err != nil {
			return "", err
		}
		resp, err := agent.Provider.Chat(
			ctx,
			[]providers.Message{{Role: "user", Content: prompt}},
			nil, // no tools for summarization
			model,
			map[string]any{
				"max_tokens":       opts.MaxTokens,
				"temperature":      opts.Temperature,
				"prompt_cache_key": "seahorse",
			},
		)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
}

// seahorseSummaryAgent resolves the agent that owns the conversation being
// summarized. Without a trusted session key it falls back to the current
// default agent, never to a captured provider snapshot.
func (al *AgentLoop) seahorseSummaryAgent(ctx context.Context) *AgentInstance {
	if al == nil || al.registry == nil {
		return nil
	}
	if sessionKey := seahorseSessionKeyFromContext(ctx); sessionKey != "" {
		if agent := al.agentForSession(sessionKey); agent != nil {
			return agent
		}
	}
	return al.registry.GetDefaultAgent()
}

// resolveTrustedConversation is the seahorse tool-side authorization boundary.
// It proves that the session belongs to the tool context's agent and maps it
// to exactly one conversation. Missing or conflicting ownership fails closed.
func (m *seahorseContextManager) resolveTrustedConversation(
	ctx context.Context,
	agentID, sessionKey string,
) (int64, error) {
	agentID = strings.TrimSpace(agentID)
	sessionKey = strings.TrimSpace(sessionKey)
	if agentID == "" || sessionKey == "" {
		return 0, fmt.Errorf("trusted agent and session identity are required")
	}
	if m == nil || m.al == nil || m.al.registry == nil {
		return 0, fmt.Errorf("agent registry is unavailable")
	}

	owners := 0
	for _, candidateID := range m.al.registry.ListAgentIDs() {
		agent, ok := m.al.registry.GetAgent(candidateID)
		if !ok || agent == nil || agent.Sessions == nil {
			continue
		}
		resolved := session.ResolveAgentID(agent.Sessions, sessionKey)
		if resolved == "" {
			continue
		}
		if !strings.EqualFold(resolved, agentID) {
			return 0, fmt.Errorf("session belongs to a different agent")
		}
		if !strings.EqualFold(agent.ID, resolved) {
			continue
		}
		owners++
	}
	if owners != 1 {
		return 0, fmt.Errorf("cannot prove exactly one owner for this session")
	}

	conversation, err := m.engine.GetRetrieval().Store().GetConversationBySessionKey(ctx, sessionKey)
	if err != nil {
		return 0, err
	}
	if conversation == nil {
		return 0, fmt.Errorf("conversation not found for this session")
	}
	return conversation.ConversationID, nil
}

// Assemble builds budget-aware context from seahorse SQLite.
func (m *seahorseContextManager) Assemble(ctx context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("seahorse assemble: nil request")
	}

	budget := req.Budget
	if budget <= 0 {
		budget = 100000
	}

	// Reserve space for model response (spec lines 1400-1410)
	effectiveBudget := budget - req.MaxTokens
	if effectiveBudget <= 0 {
		// MaxTokens >= budget is a configuration problem
		// Use 50% as minimum to avoid guaranteed overflow
		logger.WarnCF("agent", "MaxTokens >= budget, using 50% fallback",
			map[string]any{"budget": budget, "max_tokens": req.MaxTokens})
		effectiveBudget = budget / 2
	}

	result, err := m.engine.Assemble(withSeahorseSessionKey(ctx, req.SessionKey), req.SessionKey, seahorse.AssembleInput{
		Budget: effectiveBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("seahorse assemble: %w", err)
	}

	history := seahorseToProviderMessages(result)

	// Summary is already formatted as XML with system prompt addition by assembler
	return &AssembleResponse{
		History: history,
		Summary: result.Summary,
	}, nil
}

// Compact compresses conversation history via seahorse summarization.
func (m *seahorseContextManager) Compact(ctx context.Context, req *CompactRequest) error {
	if req == nil {
		return nil
	}

	// For retry (LLM overflow), use aggressive CompactUntilUnder to guarantee
	// context shrinks below budget (spec lines ~1410).
	if req.Reason == ContextCompressReasonRetry && req.Budget > 0 {
		_, err := m.engine.CompactUntilUnder(withSeahorseSessionKey(ctx, req.SessionKey), req.SessionKey, req.Budget)
		return err
	}

	_, err := m.engine.Compact(withSeahorseSessionKey(ctx, req.SessionKey), req.SessionKey, seahorse.CompactInput{
		Force:  req.Reason == ContextCompressReasonRetry,
		Budget: &req.Budget,
	})
	return err
}

// Ingest records a message into seahorse SQLite.
// All existing sessions are bootstrapped at startup, so this only ingests new messages.
func (m *seahorseContextManager) Ingest(ctx context.Context, req *IngestRequest) error {
	if req == nil {
		return nil
	}

	msg := providerToSeahorseMessage(req.Message)
	_, err := m.engine.Ingest(ctx, req.SessionKey, []seahorse.Message{msg})
	return err
}

// Clear removes all stored context for a session (seahorse DB + JSONL).
func (m *seahorseContextManager) Clear(ctx context.Context, sessionKey string) error {
	if err := m.engine.ClearSession(ctx, sessionKey); err != nil {
		return err
	}
	// The session may belong to a routed (non-default) agent whose JSONL
	// store differs from the bootstrap store, so clear the owner's store.
	sessions := m.sessions
	if m.al != nil {
		if agent := m.al.agentForSession(sessionKey); agent != nil && agent.Sessions != nil {
			sessions = agent.Sessions
		}
	}
	if sessions != nil {
		sessions.SetHistory(sessionKey, []providers.Message{})
		sessions.SetSummary(sessionKey, "")
		return sessions.Save(sessionKey)
	}
	return nil
}

// bootstrapSession reconciles JSONL session history into seahorse SQLite.
func (m *seahorseContextManager) bootstrapSession(ctx context.Context, sessionKey string) {
	if m.sessions == nil {
		return
	}

	history := m.sessions.GetHistory(sessionKey)
	if len(history) == 0 {
		return
	}

	// Convert provider messages to seahorse messages
	msgs := make([]seahorse.Message, len(history))
	for i, h := range history {
		msgs[i] = providerToSeahorseMessage(h)
	}

	if err := m.engine.Bootstrap(ctx, sessionKey, msgs); err != nil {
		logger.WarnCF("seahorse", "bootstrap", map[string]any{
			"session": sessionKey,
			"error":   err.Error(),
		})
	}
}

// providerToSeahorseMessage converts a providers.Message to a seahorse.Message.
func providerToSeahorseMessage(msg protocoltypes.Message) seahorse.Message {
	result := seahorse.Message{
		Role:             msg.Role,
		Content:          msg.Content,
		ModelName:        msg.ModelName,
		ReasoningContent: msg.ReasoningContent,
		TokenCount:       tokenizer.EstimateMessageTokens(msg),
		CreatedAt:        normalizeSeahorseMessageCreatedAt(msg.CreatedAt),
	}

	// Convert ToolCalls → MessageParts
	for _, tc := range msg.ToolCalls {
		part := seahorse.MessagePart{
			Type:       "tool_use",
			Name:       tc.Function.Name,
			Arguments:  tc.Function.Arguments,
			ToolCallID: tc.ID,
		}
		result.Parts = append(result.Parts, part)
	}

	// Convert tool result
	if msg.ToolCallID != "" {
		part := seahorse.MessagePart{
			Type:       "tool_result",
			ToolCallID: msg.ToolCallID,
			Text:       msg.Content,
		}
		result.Parts = append(result.Parts, part)
	}

	// Convert media attachments
	for _, mediaURI := range msg.Media {
		part := seahorse.MessagePart{
			Type:     "media",
			MediaURI: mediaURI,
		}
		result.Parts = append(result.Parts, part)
	}

	return result
}

func normalizeSeahorseMessageCreatedAt(createdAt *time.Time) time.Time {
	if createdAt == nil || createdAt.IsZero() {
		return time.Time{}
	}
	return createdAt.UTC().Truncate(time.Second)
}

// seahorseToProviderMessages converts a seahorse.AssembleResult to []providers.Message.
func seahorseToProviderMessages(result *seahorse.AssembleResult) []protocoltypes.Message {
	messages := make([]protocoltypes.Message, 0, len(result.Messages))

	// Convert assembled messages (which already include summary XML messages)
	for _, msg := range result.Messages {
		pm := protocoltypes.Message{
			Role:             msg.Role,
			Content:          msg.Content,
			ModelName:        msg.ModelName,
			ReasoningContent: msg.ReasoningContent,
		}

		// Reconstruct ToolCalls from parts
		for _, part := range msg.Parts {
			if part.Type == "tool_use" {
				pm.ToolCalls = append(pm.ToolCalls, protocoltypes.ToolCall{
					ID:   part.ToolCallID,
					Type: "function", // Required by OpenAI-compatible APIs (GLM, etc.)
					Function: &protocoltypes.FunctionCall{
						Name:      part.Name,
						Arguments: part.Arguments,
					},
				})
			}
			if part.Type == "tool_result" {
				pm.ToolCallID = part.ToolCallID
				if pm.Content == "" && part.Text != "" {
					pm.Content = part.Text
				}
			}
			if part.Type == "media" && part.MediaURI != "" {
				pm.Media = append(pm.Media, part.MediaURI)
			}
		}

		messages = append(messages, pm)
	}

	return messages
}

func init() {
	if err := RegisterContextManager("seahorse", newSeahorseContextManager); err != nil {
		panic(fmt.Sprintf("register seahorse context manager: %v", err))
	}
}
