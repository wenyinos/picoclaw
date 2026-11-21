package memstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// SourceLocator identifies one message that fed a forgotten memory. It stores
// locating hints and a versioned HMAC of the evidence text, never the text
// itself: the cleanup worker must be able to find the message without keeping
// a copy of the forgotten content.
type SourceLocator struct {
	SessionKey     string `json:"session_key,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	MessageID      *int64 `json:"message_id,omitempty"`
	OriginEventID  string `json:"origin_event_id,omitempty"`
	EvidenceHMAC   string `json:"evidence_hmac,omitempty"`
	KeyID          string `json:"key_id,omitempty"`
}

// CleanupOutcome reports what a backend could and could not clean. Unavailable
// locators are reported as partial, never as success.
type CleanupOutcome struct {
	Removed     int      `json:"removed"`
	Unavailable int      `json:"unavailable"`
	Sessions    int      `json:"sessions"`
	Details     []string `json:"details,omitempty"`
}

// MessageCleaner removes specific messages from a session/transcript backend.
// Implementations must return per-locator outcomes; silently claiming success
// is a contract violation.
type MessageCleaner interface {
	CleanLocators(ctx context.Context, locators []SourceLocator) (CleanupOutcome, error)
}

// sourceLocatorsLocked collects locating hints for an entry's sources before
// the rows are deleted. The evidence digest uses the active ledger key.
func (s *Store) sourceLocatorsLocked(ctx context.Context, entryID string) ([]SourceLocator, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_key,conversation_id,message_id,origin_event_id,evidence_text
		 FROM sources WHERE entry_id=?`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceLocator
	for rows.Next() {
		var loc SourceLocator
		var messageID *int64
		var evidence string
		if err := rows.Scan(&loc.SessionKey, &loc.ConversationID, &messageID,
			&loc.OriginEventID, &evidence); err != nil {
			return nil, err
		}
		loc.MessageID = messageID
		if strings.TrimSpace(evidence) != "" && s.ledger != nil {
			digest, keyID, _ := s.ledger.Digest(evidence)
			loc.EvidenceHMAC = digest
			loc.KeyID = keyID
		}
		if loc.SessionKey == "" && loc.EvidenceHMAC == "" {
			continue // nothing locatable: do not invent a target
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

// enqueueCleanupJobTx records the cross-store cleanup obligation in the same
// transaction as the entry deletion, so a crash cannot lose it.
func enqueueCleanupJobTx(ctx context.Context, tx *sql.Tx, requestID, manifest string) error {
	now := nowMillis()
	_, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO memory_jobs
		 (job_id,job_kind,input_fingerprint,generation,policy_revision,state,attempts,
		  next_run_at,priority,payload,created_at,updated_at)
		 VALUES(?,?,?,0,0,'pending',0,?,0,?,?,?)`,
		"forget:"+requestID, "forget_cleanup", requestID, now, manifest, now, now)
	return err
}

// RunCleanupJobs consumes forget_cleanup jobs and hands the manifests to a
// backend cleaner. It is safe to call on every gateway start or from the
// `memory cleanup` CLI command.
func (s *Store) RunCleanupJobs(ctx context.Context, cleaner MessageCleaner, maxJobs int) (CleanupOutcome, error) {
	total := CleanupOutcome{}
	if cleaner == nil {
		return total, fmt.Errorf("memstore: cleanup needs a backend cleaner")
	}
	if maxJobs <= 0 || maxJobs > 100 {
		maxJobs = 20
	}
	for i := 0; i < maxJobs; i++ {
		job, err := s.LeaseJobKind(ctx, "cleanup", "forget_cleanup", 0)
		if err != nil {
			return total, err
		}
		if job == nil {
			break
		}
		manifest, err := ParseManifest(job.Payload)
		if err != nil {
			_ = s.BlockJob(ctx, job.JobID, "invalid manifest: "+err.Error())
			continue
		}
		if len(manifest.Locators) == 0 {
			// Nothing locatable was recorded; mark done but keep the fact in
			// the description so an operator can see the limit.
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: no locator recorded (partial)", job.JobID))
			_ = s.CompleteJob(ctx, job.JobID)
			continue
		}
		outcome, err := cleaner.CleanLocators(ctx, manifest.Locators)
		if err != nil {
			_ = s.FailJob(ctx, job.JobID, err.Error(), 0, 5)
			continue
		}
		total.Removed += outcome.Removed
		total.Unavailable += outcome.Unavailable
		total.Sessions += outcome.Sessions
		total.Details = append(total.Details, outcome.Details...)
		if outcome.Unavailable > 0 {
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: %d locator(s) unverifiable (partial)", job.JobID, outcome.Unavailable))
		}
		_ = s.CompleteJob(ctx, job.JobID)
	}
	return total, nil
}

// ForgetManifestLocators returns the locators of one journal intent, for
// audit output.
func ParseLocators(raw string) ([]SourceLocator, error) {
	manifest, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	return manifest.Locators, nil
}

// MarshalLocators is a test/debug helper.
func MarshalLocators(locators []SourceLocator) string {
	raw, _ := json.Marshal(locators)
	return string(raw)
}
