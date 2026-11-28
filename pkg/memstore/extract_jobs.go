package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractionJobPayload references one persisted source by HMAC selector. It
// never contains the user message text.
type ExtractionJobPayload struct {
	Version        int    `json:"version"`
	SessionKey     string `json:"session_key"`
	OriginEventID  string `json:"origin_event_id"`
	OriginRevision int64  `json:"origin_revision"`
	MessageID      string `json:"message_id,omitempty"`
	Role           string `json:"role"`
	EventAt        int64  `json:"event_at,omitempty"`
	DomainID       string `json:"domain_id"`
	SourceDigest   string `json:"source_digest"`
	KeyID          string `json:"key_id"`
	HashVersion    string `json:"hash_version"`
}

// EnqueueExtractionJob persists a durable extraction reference for a completed
// user turn. The text itself remains in the session backend; only its keyed
// digest is stored in the job payload.
func (s *Store) EnqueueExtractionJob(ctx context.Context, source ExtractSource, userText string) (string, error) {
	if err := s.checkReady(); err != nil {
		return "", err
	}
	if s.ledger == nil {
		return "", fmt.Errorf("memstore: extraction needs a healthy forget ledger")
	}
	if strings.TrimSpace(source.EventID) == "" || strings.TrimSpace(source.SessionKey) == "" {
		return "", fmt.Errorf("memstore: extraction source is incomplete")
	}
	if strings.TrimSpace(userText) == "" {
		return "", nil
	}
	digest, keyID, hashVersion := s.ledger.Digest(userText)
	payload, err := json.Marshal(ExtractionJobPayload{
		Version: 1, SessionKey: source.SessionKey, OriginEventID: source.EventID,
		OriginRevision: maxInt64(source.OriginRevision, 1), MessageID: source.MessageID,
		Role: firstNonEmpty(source.Role, "user"), EventAt: source.EventAt,
		DomainID:     firstNonEmpty(source.DomainID, DomainLegacyUnassigned),
		SourceDigest: digest, KeyID: keyID, HashVersion: hashVersion,
	})
	if err != nil {
		return "", err
	}
	jobID := "extract:" + source.EventID
	if err := s.EnqueueJob(ctx, Job{
		JobID: jobID, Kind: "extraction", InputFingerprint: digest,
		Generation: source.OriginRevision, Priority: 5, Payload: string(payload),
	}); err != nil {
		return "", err
	}
	return jobID, nil
}

// LeaseExtractionJob claims one pending extraction task.
func (s *Store) LeaseExtractionJob(ctx context.Context, owner string) (*Job, error) {
	return s.LeaseJobKind(ctx, owner, "extraction", 0)
}

// MatchExtractionSource verifies that a candidate session message is the exact
// source referenced by the job payload.
func (s *Store) MatchExtractionSource(payload ExtractionJobPayload, text string) bool {
	if s.ledger == nil || strings.TrimSpace(text) == "" ||
		payload.SourceDigest == "" || payload.KeyID == "" {
		return false
	}
	if payload.KeyID != s.ledger.KeyID() {
		return false
	}
	digest, keyID, version := s.ledger.Digest(text)
	return digest == payload.SourceDigest && keyID == payload.KeyID &&
		(version == payload.HashVersion || payload.HashVersion == "")
}

// RecordExtractionReceipt writes a text-free extraction result to the
// governance operation log.
func (s *Store) RecordExtractionReceipt(ctx context.Context, jobID, status string, added int, reasonCode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now()
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "extraction", TargetEntryID: jobID, AffectedCount: added,
		Reason: "durable extraction " + status, CreatedAt: now,
		RequestID: jobID, ReasonCode: reasonCode,
	}); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
