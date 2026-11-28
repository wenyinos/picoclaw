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

// Cleanup receipt states are terminal only when the current backend state has
// been verified. "removed" means this attempt deleted at least one matching
// message; "verified_absent" means the target is absent now without claiming
// which earlier run removed it.
const (
	ReceiptPending        = "pending"
	ReceiptRunning        = "running"
	ReceiptRemoved        = "removed"
	ReceiptVerifiedAbsent = "verified_absent"
	ReceiptUnavailable    = "unavailable"
	ReceiptOutcomeUnknown = "outcome_unknown"
	ReceiptBlocked        = "blocked"
)

// LocatorReceipt is the per-backend result for one manifest locator.
type LocatorReceipt struct {
	Backend    string `json:"backend"`
	LocatorKey string `json:"locator_key"`
	State      string `json:"state"`
	ReasonCode string `json:"reason_code,omitempty"`
	Removed    int    `json:"removed,omitempty"`
}

// LocatorReceiptKey identifies a locator without storing source text or a
// reversible session locator in the receipt table.
func LocatorReceiptKey(loc SourceLocator) string {
	raw, _ := json.Marshal(struct {
		SessionKey     string `json:"session_key,omitempty"`
		ConversationID string `json:"conversation_id,omitempty"`
		MessageID      *int64 `json:"message_id,omitempty"`
		OriginEventID  string `json:"origin_event_id,omitempty"`
		EvidenceHMAC   string `json:"evidence_hmac,omitempty"`
		KeyID          string `json:"key_id,omitempty"`
		RootType       string `json:"root_type,omitempty"`
		RootVersion    int    `json:"root_version,omitempty"`
	}{
		SessionKey: loc.SessionKey, ConversationID: loc.ConversationID,
		MessageID: loc.MessageID, OriginEventID: loc.OriginEventID,
		EvidenceHMAC: loc.EvidenceHMAC, KeyID: loc.KeyID,
		RootType: loc.RootType, RootVersion: loc.RootVersion,
	})
	return ContentHash(Normalize(string(raw)))
}

// CleanupOutcome reports what a backend could and could not clean. Unavailable
// locators are reported as partial, never as success.
type CleanupOutcome struct {
	Backend        string           `json:"backend,omitempty"`
	Removed        int              `json:"removed"`
	AlreadyAbsent  int              `json:"already_absent"`
	Unavailable    int              `json:"unavailable"`
	OutcomeUnknown int              `json:"outcome_unknown"`
	Sessions       int              `json:"sessions"`
	Details        []string         `json:"details,omitempty"`
	Receipts       []LocatorReceipt `json:"receipts,omitempty"`
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

// SessionOwnership lets a multi-store cleaner skip sessions that another
// backend owns instead of falsely reporting them unavailable.
type SessionOwnership interface {
	OwnsSession(sessionKey string) bool
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

func normalizeCleanupReceipts(locators []SourceLocator, produced []LocatorReceipt,
	prior map[string]ForgetReceipt,
) ([]LocatorReceipt, int, int) {
	byKey := map[string]LocatorReceipt{}
	for _, receipt := range produced {
		if receipt.LocatorKey == "" || receipt.Backend == "" {
			continue
		}
		if existing, ok := byKey[receipt.LocatorKey]; ok {
			if receiptSeverity(receipt.State) > receiptSeverity(existing.State) {
				byKey[receipt.LocatorKey] = receipt
			}
			continue
		}
		byKey[receipt.LocatorKey] = receipt
	}
	out := make([]LocatorReceipt, 0, len(locators))
	missing := 0
	recoveredAbsent := 0
	for _, loc := range locators {
		key := LocatorReceiptKey(loc)
		receipt, ok := byKey[key]
		if !ok {
			receipt = LocatorReceipt{
				Backend: cleanupBackendForLocator(loc), LocatorKey: key,
				State: ReceiptUnavailable, ReasonCode: "no_backend_receipt",
			}
			missing++
		}
		previous := prior[key]
		if receipt.State == ReceiptUnavailable && receipt.ReasonCode == "root_no_match" &&
			(previous.State == ReceiptRunning || previous.State == ReceiptRemoved) {
			// A prior attempt reached this locator before the receipt could be
			// finalized. The current run proves the target is absent now, but it
			// cannot truthfully claim which attempt removed it.
			receipt.State = ReceiptVerifiedAbsent
			receipt.ReasonCode = "verified_after_prior_attempt"
			recoveredAbsent++
		}
		out = append(out, receipt)
	}
	return out, missing, recoveredAbsent
}

func cleanupBackendForLocator(loc SourceLocator) string {
	if strings.HasPrefix(loc.RootType, "seahorse.") {
		return "seahorse"
	}
	return "jsonl"
}

func receiptSeverity(state string) int {
	switch state {
	case ReceiptBlocked, ReceiptOutcomeUnknown, ReceiptUnavailable:
		return 3
	case ReceiptPending, ReceiptRunning:
		return 2
	case ReceiptRemoved, ReceiptVerifiedAbsent:
		return 1
	default:
		return 3
	}
}

func cleanupReceiptsIncomplete(receipts []LocatorReceipt) (unknown, unavailable int) {
	for _, receipt := range receipts {
		switch receipt.State {
		case ReceiptOutcomeUnknown:
			unknown++
		case ReceiptUnavailable, ReceiptBlocked, ReceiptPending, ReceiptRunning:
			unavailable++
		}
	}
	return unknown, unavailable
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
		if s.ledger == nil {
			total.Unavailable++
			total.Details = append(total.Details, fmt.Sprintf("job %s: forget ledger unavailable", job.JobID))
			_ = s.BlockJob(ctx, job.JobID, "forget ledger unavailable; cleanup cannot persist receipts")
			continue
		}
		requestID := strings.TrimSpace(job.InputFingerprint)
		if requestID == "" {
			requestID = strings.TrimPrefix(job.JobID, "forget:")
		}
		intent, err := s.ledger.IntentByRequestID(ctx, requestID)
		if err != nil {
			total.Unavailable++
			total.Details = append(total.Details, fmt.Sprintf("job %s: receipt lookup failed: %v", job.JobID, err))
			_ = s.FailJob(ctx, job.JobID, "receipt lookup failed", 0, 5)
			continue
		}
		if intent == nil {
			total.Unavailable++
			total.Details = append(total.Details, fmt.Sprintf("job %s: ledger intent missing", job.JobID))
			_ = s.BlockJob(ctx, job.JobID, "ledger intent missing; cleanup cannot be attributed")
			continue
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
		priorList, err := s.ledger.ReceiptsForIntent(ctx, intent.Seq)
		if err != nil {
			total.Unavailable++
			total.Details = append(total.Details, fmt.Sprintf("job %s: prior receipts unavailable", job.JobID))
			_ = s.FailJob(ctx, job.JobID, "prior receipts unavailable", 0, 5)
			continue
		}
		prior := map[string]ForgetReceipt{}
		for _, receipt := range priorList {
			prior[receipt.LocatorKey] = receipt
		}
		running := make([]LocatorReceipt, 0, len(manifest.Locators))
		for _, loc := range manifest.Locators {
			running = append(running, LocatorReceipt{
				Backend: cleanupBackendForLocator(loc), LocatorKey: LocatorReceiptKey(loc),
				State: ReceiptRunning, ReasonCode: "cleanup_started",
			})
		}
		if err := s.ledger.SaveReceipts(ctx, *intent, running, job.Attempts); err != nil {
			total.Unavailable += len(manifest.Locators)
			total.Details = append(total.Details, fmt.Sprintf("job %s: cannot persist running receipts", job.JobID))
			_ = s.FailJob(ctx, job.JobID, "cannot persist cleanup receipts", 0, 5)
			continue
		}
		outcome, err := cleaner.CleanLocators(ctx, manifest.Locators)
		if err != nil {
			total.Unavailable += len(manifest.Locators)
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: backend error: %v", job.JobID, err))
			failed := make([]LocatorReceipt, 0, len(manifest.Locators))
			for _, loc := range manifest.Locators {
				failed = append(failed, LocatorReceipt{
					Backend: cleanupBackendForLocator(loc), LocatorKey: LocatorReceiptKey(loc),
					State: ReceiptUnavailable, ReasonCode: "backend_error",
				})
			}
			_ = s.ledger.SaveReceipts(ctx, *intent, failed, job.Attempts)
			_ = s.FailJob(ctx, job.JobID, err.Error(), 0, 5)
			continue
		}

		receipts, missing, recoveredAbsent := normalizeCleanupReceipts(
			manifest.Locators, outcome.Receipts, prior)
		total.Removed += outcome.Removed
		total.AlreadyAbsent += outcome.AlreadyAbsent + recoveredAbsent
		total.Unavailable += outcome.Unavailable + missing - recoveredAbsent
		if total.Unavailable < 0 {
			total.Unavailable = 0
		}
		total.OutcomeUnknown += outcome.OutcomeUnknown
		total.Sessions += outcome.Sessions
		total.Details = append(total.Details, outcome.Details...)
		total.Receipts = append(total.Receipts, receipts...)
		if missing > 0 {
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: %d locator(s) had no backend receipt", job.JobID, missing))
		}
		if recoveredAbsent > 0 {
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: %d locator(s) verified absent after a prior attempt", job.JobID, recoveredAbsent))
		}
		if err := s.ledger.SaveReceipts(ctx, *intent, receipts, job.Attempts); err != nil {
			total.Unavailable += len(manifest.Locators)
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: cannot persist final receipts", job.JobID))
			_ = s.FailJob(ctx, job.JobID, "cannot persist cleanup receipts", 0, 5)
			continue
		}
		unknown, unavailable := cleanupReceiptsIncomplete(receipts)
		if unknown > 0 {
			reason := fmt.Sprintf("%d locator(s) have an indeterminate backend outcome", unknown)
			total.Details = append(total.Details, fmt.Sprintf("job %s: %s", job.JobID, reason))
			_ = s.BlockJob(ctx, job.JobID, reason)
			continue
		}
		if unavailable > 0 {
			reason := fmt.Sprintf("%d locator(s) unverifiable; cleanup is partial", unavailable)
			total.Details = append(total.Details,
				fmt.Sprintf("job %s: %s", job.JobID, reason))
			_ = s.BlockJob(ctx, job.JobID, reason)
			continue
		}
		if err := s.CompleteJob(ctx, job.JobID); err != nil {
			return total, err
		}
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
