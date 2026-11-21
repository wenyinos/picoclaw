package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Job states. Accepted forget work never gets dropped by capacity pressure.
const (
	JobPending = "pending"
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
	JobBlocked = "blocked"
)

// Job is one durable background unit: extraction, summary rebuild, cleanup or
// proactive-recall preparation. The payload stores references, never memory
// text.
type Job struct {
	ID               int64  `json:"id"`
	JobID            string `json:"job_id"`
	Kind             string `json:"kind"`
	InputFingerprint string `json:"input_fingerprint,omitempty"`
	Generation       int64  `json:"generation,omitempty"`
	PolicyRevision   int64  `json:"policy_revision,omitempty"`
	LeaseOwner       string `json:"lease_owner,omitempty"`
	LeaseUntil       *int64 `json:"lease_until,omitempty"`
	State            string `json:"state"`
	Attempts         int    `json:"attempts"`
	NextRunAt        *int64 `json:"next_run_at,omitempty"`
	Priority         int    `json:"priority"`
	LastError        string `json:"last_error,omitempty"`
	Payload          string `json:"payload,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

var jobPriorities = map[string]int{
	"forget_cleanup": 0,
	"revoke_cleanup": 1,
	"recovery":       1,
	"explicit_save":  2,
	"extraction":     5,
	"summary":        8,
	"embedding":      9,
}

// EnqueueJob adds a durable job. The job_id is caller-supplied and unique, so
// a reload or retry of the same request cannot double-schedule.
func (s *Store) EnqueueJob(ctx context.Context, j Job) error {
	j.JobID = strings.TrimSpace(j.JobID)
	j.Kind = strings.TrimSpace(j.Kind)
	if j.JobID == "" || j.Kind == "" {
		return fmt.Errorf("memstore: job needs id and kind")
	}
	if j.State == "" {
		j.State = JobPending
	}
	if j.Priority == 0 {
		if p, ok := jobPriorities[j.Kind]; ok {
			j.Priority = p
		} else {
			j.Priority = 5
		}
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO memory_jobs
		 (job_id,job_kind,input_fingerprint,generation,policy_revision,state,attempts,
		  next_run_at,priority,payload,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,0,?,?,?,?,?)`,
		j.JobID, j.Kind, j.InputFingerprint, j.Generation, j.PolicyRevision, j.State,
		j.NextRunAt, j.Priority, j.Payload, now, now)
	if err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}

// LeaseJob atomically claims one runnable job for owner. It skips pending
// forget work only if another lease holds it.
func (s *Store) LeaseJob(ctx context.Context, owner string, leaseFor time.Duration) (*Job, error) {
	if leaseFor <= 0 {
		leaseFor = 2 * time.Minute
	}
	now := s.now()
	until := now + leaseFor.Milliseconds()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx,
		`SELECT id,job_id,job_kind,input_fingerprint,generation,policy_revision,
		        lease_owner,lease_until,state,attempts,next_run_at,priority,last_error,payload,
		        created_at,updated_at
		 FROM memory_jobs
		 WHERE state IN ('pending','running')
		   AND (next_run_at IS NULL OR next_run_at <= ?)
		   AND (lease_until IS NULL OR lease_until < ?)
		 ORDER BY priority ASC, COALESCE(next_run_at, created_at) ASC, id ASC
		 LIMIT 1`, now, now)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE memory_jobs SET state='running', lease_owner=?, lease_until=?, attempts=attempts+1,
		 updated_at=? WHERE id=?`, owner, until, now, j.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	j.State, j.LeaseOwner, j.LeaseUntil = JobRunning, owner, &until
	j.Attempts++
	return j, nil
}

// CompleteJob marks a leased job done.
func (s *Store) CompleteJob(ctx context.Context, jobID string) error {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE memory_jobs SET state='done', lease_owner='', lease_until=NULL, updated_at=?
		 WHERE job_id=?`, now, jobID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("memstore: job %q not found", jobID)
	}
	return s.bumpRevisionLocked(ctx)
}

// FailJob schedules a retry with backoff. Exhausted attempts keep the job in
// blocked state instead of deleting an accepted obligation (FR-34).
func (s *Store) FailJob(ctx context.Context, jobID, errMsg string, backoff time.Duration, maxAttempts int) error {
	now := s.now()
	if backoff <= 0 {
		backoff = 30 * time.Second
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var attempts int
	if err := s.db.QueryRowContext(ctx,
		`SELECT attempts FROM memory_jobs WHERE job_id=?`, jobID).Scan(&attempts); err != nil {
		return fmt.Errorf("memstore: job %q: %w", jobID, err)
	}
	state := JobPending
	var next *int64
	if maxAttempts > 0 && attempts >= maxAttempts {
		state = JobBlocked
	} else {
		v := now + backoff.Milliseconds()
		next = &v
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE memory_jobs SET state=?, lease_owner='', lease_until=NULL, last_error=?,
		 next_run_at=?, updated_at=? WHERE job_id=?`,
		state, truncateRunes(strings.TrimSpace(errMsg), 200), next, now, jobID)
	if err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}

// BlockJob parks a job for human attention; the obligation stays visible.
func (s *Store) BlockJob(ctx context.Context, jobID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE memory_jobs SET state='blocked', last_error=?, lease_owner='', lease_until=NULL, updated_at=?
		 WHERE job_id=?`, truncateRunes(reason, 200), s.now(), jobID)
	return err
}

// ListJobs returns jobs, optionally filtered by state.
func (s *Store) ListJobs(ctx context.Context, state string, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,job_id,job_kind,input_fingerprint,generation,policy_revision,
	             lease_owner,lease_until,state,attempts,next_run_at,priority,last_error,payload,
	             created_at,updated_at
	      FROM memory_jobs`
	args := []any{}
	if strings.TrimSpace(state) != "" {
		q += ` WHERE state=?`
		args = append(args, state)
	}
	q += ` ORDER BY priority ASC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// JobStats returns counts per state for the dashboard.
func (s *Store) JobStats(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT state, COUNT(*) FROM memory_jobs GROUP BY state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

func scanJob(row scanner) (*Job, error) {
	var j Job
	var leaseUntil, nextRun sql.NullInt64
	err := row.Scan(&j.ID, &j.JobID, &j.Kind, &j.InputFingerprint, &j.Generation,
		&j.PolicyRevision, &j.LeaseOwner, &leaseUntil, &j.State, &j.Attempts,
		&nextRun, &j.Priority, &j.LastError, &j.Payload, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if leaseUntil.Valid {
		v := leaseUntil.Int64
		j.LeaseUntil = &v
	}
	if nextRun.Valid {
		v := nextRun.Int64
		j.NextRunAt = &v
	}
	return &j, nil
}
