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
	RootType       string `json:"root_type,omitempty"`
	RootVersion    int    `json:"root_version,omitempty"`
}

// CleanupOutcome reports what a backend could and could not clean. Unavailable
// locators are reported as partial, never as success.
type CleanupOutcome struct {
	Backend        string   `json:"backend,omitempty"`
	Removed        int      `json:"removed"`
	AlreadyAbsent  int      `json:"already_absent"`
	Unavailable    int      `json:"unavailable"`
	OutcomeUnknown int      `json:"outcome_unknown"`
	Sessions       int      `json:"sessions"`
	Details        []string `json:"details,omitempty"`
}

// EvidenceRootV1 is the host-generated locator for one complete source unit.
// The digest is HMAC(Normalize(text)) using the forget-ledger key; no text is
// stored here.
type EvidenceRootV1 struct {
	V                int    `json:"v"`
	Algo             string `json:"algo"`
	KeyID            string `json:"key_id"`
	Type             string `json:"type"`
	Canonicalization string `json:"canonicalization"`
	Digest           string `json:"digest"`
}

func encodeEvidenceRoot(rootType, keyID, digest string) (string, error) {
	if strings.TrimSpace(rootType) == "" || keyID == "" || digest == "" {
		return "", fmt.Errorf("memstore: incomplete evidence root")
	}
	raw, err := json.Marshal(EvidenceRootV1{
		V: 1, Algo: "hmac-sha256", KeyID: keyID, Type: rootType,
		Canonicalization: "normalize-v1", Digest: digest,
	})
	return string(raw), err
}

func parseEvidenceRoot(raw string) (EvidenceRootV1, bool) {
	var root EvidenceRootV1
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &root) != nil {
		return root, false
	}
	if root.V != 1 || root.Algo != "hmac-sha256" || root.KeyID == "" ||
		root.Type == "" || root.Canonicalization != "normalize-v1" || root.Digest == "" {
		return root, false
	}
	return root, true
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
		`SELECT session_key,conversation_id,message_id,origin_event_id,evidence_text,
		        evidence_root,LENGTH(evidence_text)
		 FROM sources WHERE entry_id=?`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceLocator
	for rows.Next() {
		var loc SourceLocator
		var messageID *int64
		var evidence, rootRaw string
		var evidenceLen int
		if err := rows.Scan(&loc.SessionKey, &loc.ConversationID, &messageID,
			&loc.OriginEventID, &evidence, &rootRaw, &evidenceLen); err != nil {
			return nil, err
		}
		loc.MessageID = messageID
		if root, ok := parseEvidenceRoot(rootRaw); ok {
			loc.EvidenceHMAC = root.Digest
			loc.KeyID = root.KeyID
			loc.RootType = root.Type
			loc.RootVersion = root.V
		} else if s.ledger != nil && strings.TrimSpace(evidence) != "" && evidenceLen < 120 {
			// Legacy locator: only a provably complete (untruncated) evidence
			// string can be matched against a whole stored message field.
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
	if err := s.checkReady(); err != nil {
		return CleanupOutcome{}, err
	}

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
			total.Unavailable++
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: invalid manifest: %v", job.JobID, err))
			_ = s.BlockJob(ctx, job.JobID, "invalid manifest: "+err.Error())
			continue
		}
		if len(manifest.Locators) == 0 {
			// Nothing locatable was recorded: the job must stay visible for a
			// human instead of claiming that every copy was removed.
			total.Unavailable++
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: no locator recorded (partial)", job.JobID))
			_ = s.BlockJob(ctx, job.JobID, "no locator recorded; cleanup is partial")
			continue
		}
		outcome, err := cleaner.CleanLocators(ctx, manifest.Locators)
		if err != nil {
			total.Unavailable += len(manifest.Locators)
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: backend error: %v", job.JobID, err))
			_ = s.FailJob(ctx, job.JobID, err.Error(), 0, 5)
			continue
		}
		total.Removed += outcome.Removed
		total.AlreadyAbsent += outcome.AlreadyAbsent
		total.Unavailable += outcome.Unavailable
		total.OutcomeUnknown += outcome.OutcomeUnknown
		total.Sessions += outcome.Sessions
		total.Details = append(total.Details, outcome.Details...)
		if outcome.OutcomeUnknown > 0 {
			reason := fmt.Sprintf("%d locator(s) have an indeterminate backend outcome", outcome.OutcomeUnknown)
			total.Details = append(total.Details, fmt.Sprintf("job %s: %s", job.JobID, reason))
			_ = s.BlockJob(ctx, job.JobID, reason)
			continue
		}
		if outcome.Unavailable > 0 {
			reason := fmt.Sprintf("%d locator(s) unverifiable; cleanup is partial", outcome.Unavailable)
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: %s", job.JobID, reason))
			_ = s.BlockJob(ctx, job.JobID, reason)
			continue
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
