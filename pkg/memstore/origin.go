package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// OriginEvent is the stable identity of one complete inbound source unit. It
// contains locator metadata only; the text remains in the source backend.
type OriginEvent struct {
	EventID              string `json:"event_id"`
	OriginRevision       int64  `json:"origin_revision"`
	SourceBackend        string `json:"source_backend"`
	SourceLocator        string `json:"source_locator,omitempty"`
	SessionKey           string `json:"session_key"`
	MessageID            string `json:"message_id,omitempty"`
	Role                 string `json:"role"`
	Available            bool   `json:"available"`
	EventAt              int64  `json:"event_at,omitempty"`
	CapturePolicyVersion int64  `json:"capture_policy_version"`
	DomainID             string `json:"domain_id"`
	CreatedAt            int64  `json:"created_at"`
}

// BuildOriginEventID derives an opaque, stable ID from host-controlled source
// identity components. Model-supplied values never reach this helper directly.
func BuildOriginEventID(parts ...string) string {
	clean := make([]string, len(parts))
	for i, part := range parts {
		clean[i] = strings.TrimSpace(part)
	}
	return "oe_" + ContentHash(strings.Join(clean, "\x00"))[:32]
}

// RecordOriginEvent persists one origin before a derived memory is written.
// Replaying the same event is idempotent; a different locator for the same ID
// is rejected rather than silently overwriting provenance.
func (s *Store) RecordOriginEvent(ctx context.Context, event OriginEvent) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	event.EventID = strings.TrimSpace(event.EventID)
	if event.EventID == "" {
		return fmt.Errorf("memstore: origin event needs an id")
	}
	if event.OriginRevision <= 0 {
		event.OriginRevision = 1
	}
	if event.SourceBackend == "" {
		event.SourceBackend = "jsonl"
	}
	if event.CapturePolicyVersion <= 0 {
		event.CapturePolicyVersion = 1
	}
	if event.DomainID == "" {
		event.DomainID = DomainLegacyUnassigned
	}
	if event.CreatedAt == 0 {
		event.CreatedAt = s.now()
	}
	available := 0
	if event.Available {
		available = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var existingLocator, existingSession, existingMessage string
	var existingRevision int64
	err = tx.QueryRowContext(ctx,
		`SELECT source_locator,session_key,message_id,origin_revision
		 FROM memory_origin_events WHERE event_id=?`, event.EventID).
		Scan(&existingLocator, &existingSession, &existingMessage, &existingRevision)
	if err == nil {
		if existingLocator != event.SourceLocator || existingSession != event.SessionKey ||
			existingMessage != event.MessageID {
			return fmt.Errorf("memstore: origin event %q already names a different source", event.EventID)
		}
		if existingRevision != event.OriginRevision {
			return fmt.Errorf("memstore: origin event %q revision conflict", event.EventID)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_origin_events
		 (event_id,origin_revision,source_backend,source_locator,session_key,message_id,role,
		  available,event_at,capture_policy_version,domain_id,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.EventID, event.OriginRevision, event.SourceBackend, event.SourceLocator,
		event.SessionKey, event.MessageID, event.Role, available, nullableMillis(event.EventAt),
		event.CapturePolicyVersion, event.DomainID, event.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableMillis(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

// MarkOriginUnavailable records a trusted edit/retraction signal. It does not
// delete the fact: it re-evaluates support from remaining sources and leaves
// unsupported facts for review.
func (s *Store) MarkOriginUnavailable(ctx context.Context, eventID string) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return fmt.Errorf("memstore: origin event id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE memory_origin_events SET available=0 WHERE event_id=? AND available=1`, eventID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("memstore: origin event %q not found or already unavailable", eventID)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT entry_id FROM sources WHERE origin_event_id=?`, eventID)
	if err != nil {
		return err
	}
	var entryIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		entryIDs = append(entryIDs, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	now := s.now()
	affected := 0
	for _, id := range entryIDs {
		var support int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sources s
			 LEFT JOIN memory_origin_events o ON o.event_id=s.origin_event_id
			 WHERE s.entry_id=?
			   AND (s.origin_event_id='' OR (o.event_id IS NOT NULL AND o.available=1))`,
			id).Scan(&support); err != nil {
			return err
		}
		if support > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE entries SET evidence_status='unsupported', freshness='stale',
			 row_revision=row_revision+1, updated_at=? WHERE id=?`, now, id); err != nil {
			return err
		}
		if err := s.invalidateSummariesForEntryTx(ctx, tx, id, "origin_unavailable"); err != nil {
			return err
		}
		affected++
	}
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "origin_unavailable", TargetEntryID: eventID,
		Reason: "trusted source retracted", AffectedCount: affected, CreatedAt: now,
		ReasonCode: "source_unavailable",
	}); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
