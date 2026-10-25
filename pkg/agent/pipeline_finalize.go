// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/bus"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// Finalize handles turn finalization, either:
// - Early return when allResponsesHandled=true (ExecuteTools already finalized)
// - Normal finalization for allResponsesHandled=false (sets finalContent, saves session, compact)
func (p *Pipeline) Finalize(
	ctx context.Context,
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	turnStatus TurnEndStatus,
	finalContent string,
) (turnResult, error) {
	al := p.al

	// When allResponsesHandled=true, ExecuteTools already finalized
	// (added handledToolResponseSummary, saved session, set phase to Completed).
	// But still check for hard abort - if requested, abort the turn.
	if exec.allResponsesHandled {
		if ts.hardAbortRequested() {
			return al.abortTurn(ts)
		}
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{
			finalContent: finalContent,
			modelName:    exec.llmModelName,
			status:       turnStatus,
			followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
		}, nil
	}

	ts.setPhase(TurnPhaseFinalizing)
	ts.setFinalContent(finalContent)
	if !ts.opts.NoHistory {
		finalMsg := providers.Message{
			Role:             "assistant",
			Content:          finalContent,
			ModelName:        exec.llmModelName,
			ReasoningContent: responseReasoningContent(exec.response),
		}
		ts.agent.Sessions.AddFullMessage(ts.sessionKey, finalMsg)
		ts.recordPersistedMessage(finalMsg)
		ts.ingestMessage(turnCtx, al, finalMsg)
	}

	// Publish the final frame before persistence and compaction. The client
	// should stop typing as soon as the model is done; disk I/O and summary
	// generation must not delay the last content update. Usage still includes
	// the final assistant message because it was added above.
	contextUsage := computeContextUsage(ts.agent, ts.sessionKey)
	streamErr := finalizeConfiguredStreamingLLM(turnCtx, ts, exec, finalContent, contextUsage)

	var saveErr error
	if !ts.opts.NoHistory {
		if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
			al.emitEvent(
				runtimeevents.KindAgentError,
				ts.eventMeta("runTurn", "turn.error"),
				ErrorPayload{
					Stage:   "session_save",
					Message: err.Error(),
				},
			)
			saveErr = err
		}
	}

	if !ts.opts.NoHistory && ts.opts.EnableSummary {
		al.contextManager.Compact(
			turnCtx,
			&CompactRequest{
				SessionKey: ts.sessionKey,
				Reason:     ContextCompressReasonSummarize,
				Budget:     ts.agent.ContextWindow,
			},
		)
	}

	// Extract durable memories after the response path; failures never affect
	// the turn result.
	p.al.extractMemoryAsync(ts.agent.Workspace, p.Cfg.Memory, ts.userMessage, finalContent, ts.sessionKey)

	// If streaming never became visible, keep the legacy Pico interim publish path
	// so the final answer is still delivered outside normal SendResponse.
	if ((streamErr != nil && !isConfiguredStreamingVisibleError(streamErr)) || exec.streamingFallback) &&
		!ts.opts.SendResponse && ts.opts.AllowInterimPicoPublish && finalContent != "" {
		msg := outboundMessageForTurnWithOptions(ts, finalContent, outboundTurnMessageOptions{
			modelName: exec.llmModelName,
		})
		msg.ContextUsage = contextUsage
		markFinalOutbound(&msg)
		_ = al.bus.PublishOutbound(turnCtx, msg)
	}
	if streamErr != nil && isConfiguredStreamingVisibleError(streamErr) {
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{
			finalContent: finalContent,
			status:       TurnEndStatusError,
			followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
		}, streamErr
	}
	if saveErr != nil {
		ts.setPhase(TurnPhaseCompleted)
		return turnResult{status: TurnEndStatusError}, saveErr
	}
	ts.setPhase(TurnPhaseCompleted)
	return turnResult{
		finalContent: finalContent,
		modelName:    exec.llmModelName,
		status:       turnStatus,
		followUps:    append([]bus.InboundMessage(nil), ts.followUps...),
	}, nil
}
