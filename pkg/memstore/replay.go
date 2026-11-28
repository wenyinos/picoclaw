package memstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Replay target and intent states are reported to operators without exposing
// memory text.
const (
	ReplayStateEligible       = "eligible"
	ReplayStateCleanupOnly    = "cleanup_only"
	ReplayStateBlocked        = "blocked"
	ReplayStateUnapproved     = "unapproved"
	ReplayStateUnsupported    = "unsupported"
	ReplayTargetAbsent        = "absent"
	ReplayTargetRevisionMatch = "revision_match"
	ReplayTargetDigestMatch   = "digest_match"
)

// ForgetReplayTargetPreview describes one manifest target.
type ForgetReplayTargetPreview struct {
	EntryID          string `json:"entry_id"`
	DomainID         string `json:"domain_id"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	State            string `json:"state"`
	ReasonCode       string `json:"reason_code,omitempty"`
}

// ForgetReplayIntentPreview describes one ledger intent and why it may or may
// not be replayed.
type ForgetReplayIntentPreview struct {
	Seq         int64                       `json:"seq"`
	RequestID   string                      `json:"request_id"`
	Status      string                      `json:"status"`
	DomainID    string                      `json:"domain_id"`
	State       string                      `json:"state"`
	ReasonCode  string                      `json:"reason_code,omitempty"`
	Targets     []ForgetReplayTargetPreview `json:"targets,omitempty"`
	CleanupOnly bool                        `json:"cleanup_only,omitempty"`
}

// ForgetReplayPreview is a read-only account of what a replay would do.
type ForgetReplayPreview struct {
	Scanned          int                         `json:"scanned"`
	Eligible         int                         `json:"eligible"`
	Blocked          int                         `json:"blocked"`
	Unapproved       int                         `json:"unapproved"`
	Unsupported      int                         `json:"unsupported"`
	AlreadyAbsent    int                         `json:"already_absent"`
	EntriesRemovable int                         `json:"entries_removable"`
	Intents          []ForgetReplayIntentPreview `json:"intents"`
}

// ForgetReplayResult reports only work that was actually committed.
type ForgetReplayResult struct {
	Removed       int `json:"removed"`
	AlreadyAbsent int `json:"already_absent"`
	CleanupJobs   int `json:"cleanup_jobs"`
	Blocked       int `json:"blocked"`
	Unapproved    int `json:"unapproved"`
	Unsupported   int `json:"unsupported"`
}

type replayIntentPlan struct {
	intent    ForgetIntent
	manifest  ForgetTargetManifest
	clusters  map[string][]string
	preview   ForgetReplayIntentPreview
	removable int
	absent    int
}

// PreviewForgetReplay evaluates every ledger intent without changing either
// database. It is the only API/CLI preview path.
func (s *Store) PreviewForgetReplay(ctx context.Context) (*ForgetReplayPreview, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, _, err := s.buildForgetReplayPlanLocked(ctx)
	return preview, err
}

// ExecuteForgetReplay re-applies only eligible intents. Callers must obtain an
// explicit owner confirmation before invoking it; the API and CLI do so via
// dry-run-first contracts. blocked intents are never eligible, even if replay
// is called directly.
func (s *Store) ExecuteForgetReplay(ctx context.Context) (*ForgetReplayResult, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	preview, plans, err := s.buildForgetReplayPlanLocked(ctx)
	if err != nil {
		return nil, err
	}
	result := &ForgetReplayResult{
		AlreadyAbsent: preview.AlreadyAbsent,
		Blocked:       preview.Blocked,
		Unapproved:    preview.Unapproved,
		Unsupported:   preview.Unsupported,
	}
	if len(plans) == 0 {
		return result, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	appliedSeqs := make([]int64, 0, len(plans))
	for _, item := range plans {
		affected := 0
		for _, target := range item.preview.Targets {
			if target.State != ReplayTargetRevisionMatch &&
				target.State != ReplayTargetDigestMatch {
				continue
			}
			id := target.EntryID
			if err := s.redactSummariesForEntryTx(ctx, tx, id, item.clusters[id], now); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM supersedes WHERE entry_id=? OR superseded_id=?`, id, id); err != nil {
				return nil, err
			}
			for _, key := range item.manifest.RelationshipKeys {
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM relationship_state WHERE key=?`, key); err != nil {
					return nil, err
				}
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM relationship_state_history WHERE key=?`, key); err != nil {
					return nil, err
				}
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM relationship_state WHERE source_event_id=?`, "entry:"+id); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM relationship_state_history WHERE source_event_id=?`, "entry:"+id); err != nil {
				return nil, err
			}
			res, err := tx.ExecContext(ctx,
				`DELETE FROM entries WHERE id=? AND domain_id=?`, id, target.DomainID)
			if err != nil {
				return nil, err
			}
			n, _ := res.RowsAffected()
			affected += int(n)
		}
		if affected > 0 {
			result.Removed += affected
			if err := s.logOperation(ctx, tx, OperationLog{
				Operation: "replay_forget", Reason: "ledger manifest replay",
				AffectedCount: affected, CreatedAt: now,
				RequestID: item.intent.RequestID, ReasonCode: "recovery",
			}); err != nil {
				return nil, err
			}
		}
		if err := enqueueReplayCleanupJobTx(ctx, tx, item.intent.RequestID,
			MarshalManifest(item.manifest)); err != nil {
			return nil, err
		}
		result.CleanupJobs++
		appliedSeqs = append(appliedSeqs, item.intent.Seq)
	}
	if result.Removed > 0 {
		if err := s.bumpRevision(ctx, tx); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	revision := s.revision.Load()
	for _, seq := range appliedSeqs {
		if err := s.ledger.MarkApplied(ctx, seq, revision); err != nil {
			return result, fmt.Errorf("memstore: mark replay applied: %w", err)
		}
	}
	return result, nil
}

// ReplayForget is retained for maintenance callers that only need the removed
// count. It uses the same revision/digest/status checks as ExecuteForgetReplay.
func (s *Store) ReplayForget(ctx context.Context) (int, error) {
	result, err := s.ExecuteForgetReplay(ctx)
	if err != nil {
		return 0, err
	}
	return result.Removed, nil
}

// buildForgetReplayPlanLocked evaluates all intents atomically with respect to
// Store mutations. It must be called with s.mu held.
func (s *Store) buildForgetReplayPlanLocked(ctx context.Context) (*ForgetReplayPreview, []replayIntentPlan, error) {
	if s.ledger == nil {
		return nil, nil, fmt.Errorf("memstore: forget ledger unavailable")
	}
	intents, err := s.ledger.AllIntents(ctx)
	if err != nil {
		return nil, nil, err
	}
	preview := &ForgetReplayPreview{Scanned: len(intents)}
	plans := make([]replayIntentPlan, 0, len(intents))
	for _, intent := range intents {
		item := ForgetReplayIntentPreview{
			Seq: intent.Seq, RequestID: intent.RequestID, Status: intent.Status,
			DomainID: strings.TrimSpace(intent.DomainID),
		}
		if item.DomainID == "" {
			item.DomainID = DomainLegacyUnassigned
		}
		appendIntent := func() {
			preview.Intents = append(preview.Intents, item)
		}

		switch strings.ToLower(strings.TrimSpace(intent.Status)) {
		case "blocked":
			item.State, item.ReasonCode = ReplayStateBlocked, "intent_blocked"
			preview.Blocked++
			appendIntent()
			continue
		case "pending":
			var scope pendingScope
			if json.Unmarshal([]byte(intent.ScopeJSON), &scope) != nil ||
				strings.TrimSpace(scope.Actor) == "" {
				item.State, item.ReasonCode = ReplayStateUnapproved, "approval_metadata_missing"
				preview.Unapproved++
				appendIntent()
				continue
			}
		case "applied":
			// Applied means the main phase was committed before a restore; the
			// target still needs proof before it can be deleted from the restored copy.
		default:
			item.State, item.ReasonCode = ReplayStateUnsupported, "unsupported_intent_status"
			preview.Unsupported++
			appendIntent()
			continue
		}

		manifest, parseErr := ParseManifest(intent.TargetManifest)
		if parseErr != nil || len(manifest.Targets) == 0 {
			item.State, item.ReasonCode = ReplayStateBlocked, "manifest_unreplayable"
			preview.Blocked++
			appendIntent()
			continue
		}
		var scope pendingScope
		_ = json.Unmarshal([]byte(intent.ScopeJSON), &scope)

		conflict := ""
		targets := make([]ForgetReplayTargetPreview, 0, len(manifest.Targets))
		clusters := map[string][]string{}
		removable := 0
		absent := 0
		for _, target := range manifest.Targets {
			domainID := strings.TrimSpace(target.DomainID)
			if domainID == "" {
				domainID = item.DomainID
			}
			tp := ForgetReplayTargetPreview{
				EntryID: target.EntryID, DomainID: domainID,
				ExpectedRevision: target.ExpectedRevision,
			}
			var rowDomain, text string
			var revision int64
			err := s.db.QueryRowContext(ctx,
				`SELECT domain_id,row_revision,text FROM entries WHERE id=?`,
				target.EntryID).Scan(&rowDomain, &revision, &text)
			if errors.Is(err, sql.ErrNoRows) {
				tp.State = ReplayTargetAbsent
				absent++
				targets = append(targets, tp)
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			if rowDomain != domainID {
				tp.State, tp.ReasonCode = ReplayStateBlocked, "domain_mismatch"
				targets = append(targets, tp)
				conflict = "domain_mismatch"
				continue
			}
			expected := target.ExpectedRevision
			if expected == 0 {
				expected = scope.ExpectedRevision
			}
			if expected > 0 {
				if revision != expected {
					tp.State, tp.ReasonCode = ReplayStateBlocked, "revision_changed"
					targets = append(targets, tp)
					conflict = "revision_changed"
					continue
				}
				tp.State = ReplayTargetRevisionMatch
			} else {
				digest := ""
				if s.ledger != nil {
					digest, _, _ = s.ledger.Digest(text)
				}
				if digest == "" || digest != intent.SelectorDigest {
					tp.State, tp.ReasonCode = ReplayStateBlocked, "content_proof_mismatch"
					targets = append(targets, tp)
					conflict = "content_proof_mismatch"
					continue
				}
				tp.State = ReplayTargetDigestMatch
			}
			cs, clusterErr := s.clusterIDsLocked(ctx, target.EntryID)
			if clusterErr != nil {
				return nil, nil, clusterErr
			}
			clusters[target.EntryID] = cs
			removable++
			targets = append(targets, tp)
		}
		item.Targets = targets
		if conflict != "" {
			item.State, item.ReasonCode = ReplayStateBlocked, conflict
			preview.Blocked++
			appendIntent()
			continue
		}
		item.State = ReplayStateEligible
		item.CleanupOnly = removable == 0
		if item.CleanupOnly {
			item.State = ReplayStateCleanupOnly
		}
		preview.Eligible++
		preview.AlreadyAbsent += absent
		preview.EntriesRemovable += removable
		appendIntent()
		plans = append(plans, replayIntentPlan{
			intent: intent, manifest: manifest, clusters: clusters,
			preview: item, removable: removable, absent: absent,
		})
	}
	return preview, plans, nil
}
