package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memstore"
)

// runExtractionJobs drains durable extraction references. The user message is
// reloaded from the persisted session and verified against the job's keyed
// digest; no chat text is stored in the job payload.
func (al *AgentLoop) runExtractionJobs(parent context.Context) {
	if al == nil || al.cfg == nil || !al.extractionRunning.CompareAndSwap(false, true) {
		return
	}
	defer al.extractionRunning.Store(false)
	workspace := strings.TrimSpace(al.cfg.Agents.Defaults.Workspace)
	if workspace == "" {
		return
	}
	store, err := memstore.OpenForWorkspace(workspace, memstoreConfigFrom(al.cfg.Memory))
	if err != nil {
		logger.WarnCF("memory", "extraction worker skipped: store unavailable", map[string]any{
			"error": err.Error(),
		})
		return
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	for i := 0; i < 20; i++ {
		job, err := store.LeaseExtractionJob(ctx, "agent-extraction")
		if err != nil {
			logger.WarnCF("memory", "extraction job lease failed", map[string]any{"error": err.Error()})
			return
		}
		if job == nil {
			return
		}
		var payload memstore.ExtractionJobPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil || payload.Version != 1 ||
			payload.SessionKey == "" || payload.OriginEventID == "" {
			_ = store.BlockJob(ctx, job.JobID, "invalid extraction payload")
			_ = store.RecordExtractionReceipt(ctx, job.JobID, "blocked", 0, "extraction_payload_invalid")
			continue
		}
		agent := al.agentForSession(payload.SessionKey)
		if agent == nil || agent.Sessions == nil {
			_ = store.FailJob(ctx, job.JobID, "extraction session owner unavailable", 30*time.Second, 5)
			continue
		}
		history := agent.Sessions.GetHistory(payload.SessionKey)
		sourceText := ""
		for i := len(history) - 1; i >= 0; i-- {
			msg := history[i]
			if msg.Role != "user" {
				continue
			}
			if store.MatchExtractionSource(payload, msg.Content) {
				sourceText = msg.Content
				break
			}
		}
		if sourceText == "" {
			_ = store.FailJob(ctx, job.JobID, "extraction source message unavailable", 30*time.Second, 5)
			continue
		}
		eventAt := payload.EventAt
		if eventAt <= 0 {
			eventAt = time.Now().UnixMilli()
		}
		results, err := store.ExtractTurnWithSource(ctx, memstore.ExtractSource{
			EventID: payload.OriginEventID, OriginRevision: payload.OriginRevision,
			Backend: "jsonl", Locator: payload.SessionKey, SessionKey: payload.SessionKey,
			MessageID: payload.MessageID, Role: "user", EventAt: eventAt,
			DomainID: payload.DomainID,
		}, sourceText, "", time.UnixMilli(eventAt), al.cfg.Memory.ReviewRequired)
		if err != nil {
			if errors.Is(err, memstore.ErrMemoryNotReady) {
				_ = store.FailJob(ctx, job.JobID, "waiting for memory recovery", 2*time.Minute, 0)
			} else {
				_ = store.FailJob(ctx, job.JobID, err.Error(), 30*time.Second, 5)
			}
			continue
		}
		if err := store.RecordExtractionReceipt(ctx, job.JobID, "completed", len(results),
			"extraction_completed"); err != nil {
			_ = store.FailJob(ctx, job.JobID, "extraction receipt failed", 30*time.Second, 5)
			continue
		}
		if err := store.CompleteJob(ctx, job.JobID); err != nil {
			logger.WarnCF("memory", "extraction job completion failed", map[string]any{
				"job_id": job.JobID, "error": err.Error(),
			})
			return
		}
		if len(results) > 0 {
			logger.InfoCF("memory", "memories extracted", map[string]any{
				"count": len(results), "session": payload.SessionKey,
			})
		}
	}
}
