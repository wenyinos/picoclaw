package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Commitment lifecycle. Due dates never turn into "broken" automatically:
// only the user's explicit statement can fulfill or cancel a promise.
const (
	CommitmentProposed  = "proposed"
	CommitmentConfirmed = "confirmed"
	CommitmentFulfilled = "fulfilled"
	CommitmentCancelled = "cancelled"
)

// Commitment is one structured promise with explicit evidence.
type Commitment struct {
	ID            string `json:"id"`
	DomainID      string `json:"domain_id,omitempty"`
	Text          string `json:"text"`
	Status        string `json:"status"`
	DueAt         *int64 `json:"due_at,omitempty"`
	DueTimezone   string `json:"due_timezone,omitempty"`
	FulfilledBy   string `json:"fulfilled_by,omitempty"`
	SourceEventID string `json:"source_event_id,omitempty"`
	Revision      int64  `json:"revision"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// CreateCommitment records a proposed promise. Model inference alone is not
// enough: the source event id must reference a real message when provided.
func (s *Store) CreateCommitment(ctx context.Context, c Commitment) (*Commitment, error) {
	if strings.TrimSpace(c.Text) == "" {
		return nil, fmt.Errorf("memstore: commitment text is required")
	}
	if c.ID == "" {
		c.ID = "commit:" + randomUUID()[:8]
	}
	if c.DomainID == "" {
		c.DomainID = DomainLegacyUnassigned
	}
	if c.Status == "" {
		c.Status = CommitmentProposed
	}
	if !validCommitmentStatus(c.Status) {
		return nil, fmt.Errorf("memstore: invalid commitment status %q", c.Status)
	}
	if c.Status == CommitmentFulfilled || c.Status == CommitmentCancelled {
		return nil, fmt.Errorf("memstore: a commitment cannot be created in a terminal state")
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_commitments
		 (id,domain_id,text,status,due_at,due_timezone,fulfilled_by,source_event_id,revision,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,1,?,?)`,
		c.ID, c.DomainID, strings.TrimSpace(c.Text), c.Status, c.DueAt,
		c.DueTimezone, c.FulfilledBy, c.SourceEventID, now, now)
	if err != nil {
		return nil, err
	}
	if err := s.bumpRevisionLocked(ctx); err != nil {
		return nil, err
	}
	c.Revision, c.CreatedAt, c.UpdatedAt = 1, now, now
	return &c, nil
}

// UpdateCommitmentStatus applies a lifecycle transition. Fulfilled and
// cancelled require user evidence: a bare due date cannot move the state.
func (s *Store) UpdateCommitmentStatus(ctx context.Context, id, status, evidence string) (*Commitment, error) {
	if !validCommitmentStatus(status) {
		return nil, fmt.Errorf("memstore: invalid commitment status %q", status)
	}
	if (status == CommitmentFulfilled || status == CommitmentCancelled) && strings.TrimSpace(evidence) == "" {
		return nil, fmt.Errorf("memstore: %s needs explicit user evidence", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE memory_commitments SET status=?, fulfilled_by=?, revision=revision+1, updated_at=?
		 WHERE id=? AND status NOT IN ('fulfilled','cancelled')`,
		status, strings.TrimSpace(evidence), s.now(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("memstore: commitment %q not found or already closed", id)
	}
	if err := s.bumpRevisionLocked(ctx); err != nil {
		return nil, err
	}
	return s.GetCommitment(ctx, id)
}

// GetCommitment loads one promise.
func (s *Store) GetCommitment(ctx context.Context, id string) (*Commitment, error) {
	var c Commitment
	var due sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id,domain_id,text,status,due_at,due_timezone,fulfilled_by,source_event_id,
		        revision,created_at,updated_at
		 FROM memory_commitments WHERE id=?`, id).
		Scan(&c.ID, &c.DomainID, &c.Text, &c.Status, &due, &c.DueTimezone,
			&c.FulfilledBy, &c.SourceEventID, &c.Revision, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memstore: commitment %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	if due.Valid {
		v := due.Int64
		c.DueAt = &v
	}
	return &c, nil
}

// ListCommitments returns promises for one domain.
func (s *Store) ListCommitments(ctx context.Context, domainID string, includeClosed bool) ([]Commitment, error) {
	if strings.TrimSpace(domainID) == "" {
		domainID = DomainLegacyUnassigned
	}
	q := `SELECT id,domain_id,text,status,due_at,due_timezone,fulfilled_by,source_event_id,
	             revision,created_at,updated_at
	      FROM memory_commitments WHERE domain_id=?`
	if !includeClosed {
		q += ` AND status NOT IN ('fulfilled','cancelled')`
	}
	q += ` ORDER BY COALESCE(due_at, created_at), created_at`
	rows, err := s.db.QueryContext(ctx, q, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Commitment{}
	for rows.Next() {
		var c Commitment
		var due sql.NullInt64
		if err := rows.Scan(&c.ID, &c.DomainID, &c.Text, &c.Status, &due, &c.DueTimezone,
			&c.FulfilledBy, &c.SourceEventID, &c.Revision, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if due.Valid {
			v := due.Int64
			c.DueAt = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func validCommitmentStatus(status string) bool {
	switch status {
	case CommitmentProposed, CommitmentConfirmed, CommitmentFulfilled, CommitmentCancelled:
		return true
	}
	return false
}

// Recall delivery states. prepared/sending/sent/unknown/failed/cancelled are
// explicit so a timeout never looks like success.
const (
	RecallCandidate = "candidate"
	RecallPrepared  = "prepared"
	RecallSent      = "sent"
	RecallUnknown   = "delivery_unknown"
	RecallFailed    = "failed"
	RecallCancelled = "cancelled"
)

// Recall is one proactive-memory candidate or delivery record. It stores
// references, never memory text: jobs.json must not become a second copy.
type Recall struct {
	ID          string `json:"id"`
	DomainID    string `json:"domain_id,omitempty"`
	Topic       string `json:"topic,omitempty"`
	EntryIDs    string `json:"entry_ids,omitempty"`
	TargetRef   string `json:"target_ref,omitempty"`
	DedupKey    string `json:"dedup_key,omitempty"`
	Status      string `json:"status"`
	TriggerKind string `json:"trigger_kind,omitempty"`
	ScheduledAt *int64 `json:"scheduled_at,omitempty"`
	SentAt      *int64 `json:"sent_at,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// CreateRecall records a candidate. DedupKey is unique so a reload cannot
// enqueue the same recall twice.
func (s *Store) CreateRecall(ctx context.Context, r Recall) (*Recall, error) {
	if r.ID == "" {
		r.ID = "recall:" + randomUUID()[:8]
	}
	if r.DomainID == "" {
		r.DomainID = DomainLegacyUnassigned
	}
	if r.Status == "" {
		r.Status = RecallCandidate
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO memory_recalls
		 (id,domain_id,topic,entry_ids,target_ref,dedup_key,status,trigger_kind,scheduled_at,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.DomainID, r.Topic, r.EntryIDs, r.TargetRef, r.DedupKey, r.Status,
		r.TriggerKind, r.ScheduledAt, now)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("memstore: duplicate recall for dedup key")
	}
	if err := s.bumpRevisionLocked(ctx); err != nil {
		return nil, err
	}
	r.CreatedAt = now
	return &r, nil
}

// ListRecalls returns recall rows for one domain.
func (s *Store) ListRecalls(ctx context.Context, domainID string, limit int) ([]Recall, error) {
	if strings.TrimSpace(domainID) == "" {
		domainID = DomainLegacyUnassigned
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,domain_id,topic,entry_ids,target_ref,dedup_key,status,trigger_kind,
		        scheduled_at,sent_at,created_at
		 FROM memory_recalls WHERE domain_id=? ORDER BY created_at DESC LIMIT ?`,
		domainID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Recall{}
	for rows.Next() {
		var r Recall
		var scheduled, sent sql.NullInt64
		if err := rows.Scan(&r.ID, &r.DomainID, &r.Topic, &r.EntryIDs, &r.TargetRef,
			&r.DedupKey, &r.Status, &r.TriggerKind, &scheduled, &sent, &r.CreatedAt); err != nil {
			return nil, err
		}
		if scheduled.Valid {
			v := scheduled.Int64
			r.ScheduledAt = &v
		}
		if sent.Valid {
			v := sent.Int64
			r.SentAt = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkRecallStatus transitions a recall. A recall whose target or source no
// longer authorizes delivery moves to cancelled instead of being sent.
func (s *Store) MarkRecallStatus(ctx context.Context, id, status string) error {
	switch status {
	case RecallCandidate, RecallPrepared, RecallSent, RecallUnknown, RecallFailed, RecallCancelled:
	default:
		return fmt.Errorf("memstore: invalid recall status %q", status)
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	q := `UPDATE memory_recalls SET status=?`
	args := []any{status}
	if status == RecallSent {
		q += `, sent_at=?`
		args = append(args, now)
	}
	q += ` WHERE id=?`
	args = append(args, id)
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("memstore: recall %q not found", id)
	}
	return s.bumpRevisionLocked(ctx)
}

// RecordRecallFeedback stores the user's reaction. Correction and forget
// feedback must still go through the governance entry points.
func (s *Store) RecordRecallFeedback(ctx context.Context, recallID, feedback string) error {
	if strings.TrimSpace(feedback) == "" {
		return fmt.Errorf("memstore: empty feedback")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_recall_feedback(recall_id,feedback,created_at) VALUES(?,?,?)`,
		recallID, strings.TrimSpace(feedback), s.now()); err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}
