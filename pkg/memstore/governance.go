package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Inspect returns the evidence chain for one memory: sources, relations,
// supersede edges, governance log and cluster membership.
func (s *Store) Inspect(ctx context.Context, id string) (*InspectResult, error) {
	e, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &InspectResult{Entry: *e, Sources: e.Sources}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id,source_id,target_id,relation_type,confidence,source_type,created_at
		 FROM memory_relations WHERE source_id=? OR target_id=? ORDER BY created_at`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.ID, &r.SourceID, &r.TargetID, &r.RelationType,
			&r.Confidence, &r.SourceType, &r.CreatedAt); err != nil {
			return nil, err
		}
		if r.RelationType == RelSupersedes {
			out.Supersedes = append(out.Supersedes, r)
		} else {
			out.Relations = append(out.Relations, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ops, err := s.ListOperations(ctx, 200)
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		if op.TargetEntryID == id || op.ReplacementEntryID == id {
			out.Operations = append(out.Operations, op)
		}
	}

	clusterRows, err := s.db.QueryContext(ctx,
		`SELECT cluster_id FROM memory_cluster_members WHERE entry_id=? ORDER BY cluster_id`, id)
	if err != nil {
		return nil, err
	}
	defer clusterRows.Close()
	for clusterRows.Next() {
		var c string
		if err := clusterRows.Scan(&c); err != nil {
			return nil, err
		}
		out.Clusters = append(out.Clusters, c)
	}
	return out, clusterRows.Err()
}

// Confirm marks a memory as user-confirmed: it gains a confirmed source,
// confidence rises to 0.95 and pending_review entries become active.
func (s *Store) Confirm(ctx context.Context, id, evidence string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UnixMilli()
	res, err := tx.ExecContext(ctx,
		`UPDATE entries SET status=CASE WHEN status='pending_review' THEN 'active' ELSE status END,
		 confidence=MAX(confidence,0.95), updated_at=? WHERE id=?`, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("memstore: entry %q not found", id)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO sources(entry_id,source_type,source_id,session_key,
			evidence_text,confidence,user_confirmed,created_at)
		 VALUES(?,'confirmed','confirm','',?,0.95,1,?)`,
		id, truncateRunes(strings.TrimSpace(evidence), 120), now); err != nil {
		return err
	}
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "confirm", TargetEntryID: id, Reason: "user confirmed", AffectedCount: 1, CreatedAt: now,
	}); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Correct replaces a memory with corrected text. The old entry is kept as
// superseded history with valid_to set, and the replacement is linked.
func (s *Store) Correct(ctx context.Context, id string, in AddInput) (*AddResult, error) {
	old, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if old.PinnedCore && !in.Force {
		return nil, fmt.Errorf("memstore: entry %q is pinned_core, pass force=true", id)
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("memstore: correct needs text")
	}
	in.SupersedesID = id
	in.Force = true
	in.BypassForgetTombstone = true
	if in.Realm == "" {
		in.Realm = old.Realm
	}
	if in.Layer == "" {
		in.Layer = old.Layer
	}
	if in.Subject == "" {
		in.Subject = old.Subject
	}
	if in.Predicate == "" {
		in.Predicate = old.Predicate
	}
	if in.Facet == "" {
		in.Facet = old.Facet
	}
	if in.Scope == "" {
		in.Scope = old.Scope
	}
	if in.Audience == "" {
		in.Audience = old.Audience
	}
	if in.SourceType == "" {
		in.SourceType = SourceManual
	}
	if in.EvidenceText == "" {
		in.EvidenceText = in.Text
	}
	res, err := s.Add(ctx, in)
	if err != nil {
		return nil, err
	}
	if res.Entry != nil {
		now := time.Now().UnixMilli()
		s.mu.Lock()
		tx, txErr := s.db.BeginTx(ctx, nil)
		if txErr == nil {
			txErr = s.logOperation(ctx, tx, OperationLog{
				Operation: "correct", TargetEntryID: id, ReplacementEntryID: res.Entry.ID,
				Reason: "user correction", AffectedCount: 1, CreatedAt: now,
			})
			if txErr == nil {
				txErr = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		s.mu.Unlock()
		if txErr != nil {
			return res, txErr
		}
	}
	return res, nil
}

// Forget hard-deletes a memory and its derived data. dryRun only reports the
// impact. The operation log keeps a content hash so the same sentence cannot
// be extracted again, but never stores the text itself.
func (s *Store) Forget(ctx context.Context, id string, dryRun bool) (*ForgetPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var hash, status string
	err := s.db.QueryRowContext(ctx, `SELECT content_hash,status FROM entries WHERE id=?`, id).
		Scan(&hash, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return &ForgetPreview{EntryID: id, AlreadyGone: true}, nil
	}
	if err != nil {
		return nil, err
	}

	clusters, err := s.clusterIDsLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	preview := &ForgetPreview{EntryID: id, ContentHash: hash, ClusterIDs: clusters}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sources WHERE entry_id=?`, id).Scan(&preview.Sources)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_relations WHERE source_id=? OR target_id=?`, id, id).
		Scan(&preview.Relations)
	preview.Summaries = s.activeSummaryCountLocked(ctx, clusters)
	if dryRun {
		return preview, nil
	}

	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Order matters: snapshot clusters first, then invalidate summaries, then
	// delete the entry (cascades clear members), and only then log.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM supersedes WHERE entry_id=? OR superseded_id=?`, id, id); err != nil {
		return nil, err
	}
	if len(clusters) > 0 {
		args := []any{now}
		for _, c := range clusters {
			args = append(args, c)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE memory_summary_versions SET status='invalidated', superseded_at=?
			 WHERE status='active' AND cluster_id IN (`+placeholders(len(clusters))+`)`, args...); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE id=?`, id); err != nil {
		return nil, err
	}
	affected := preview.Sources + preview.Relations + preview.Summaries + 1
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "forget", TargetEntryID: id, ContentHash: hash,
		Reason: "user forget", AffectedCount: affected, CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}

// ReplayForget re-applies every forget tombstone to entries that came back
// after restoring an older backup.
func (s *Store) ReplayForget(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT content_hash FROM memory_operations
		 WHERE operation='forget' AND content_hash<>''`)
	if err != nil {
		return 0, err
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return 0, err
		}
		hashes = append(hashes, h)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	for _, hash := range hashes {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM entries WHERE content_hash=?`, hash)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	if total > 0 {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='revision'`); err != nil {
			return total, err
		}
		s.revision.Add(1)
	}
	return total, nil
}

// ListOperations returns the governance log, newest first.
func (s *Store) ListOperations(ctx context.Context, limit int) ([]OperationLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,operation,target_entry_id,replacement_entry_id,content_hash,
		        reason,affected_count,created_at
		 FROM memory_operations ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationLog
	for rows.Next() {
		var op OperationLog
		if err := rows.Scan(&op.ID, &op.Operation, &op.TargetEntryID, &op.ReplacementEntryID,
			&op.ContentHash, &op.Reason, &op.AffectedCount, &op.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *Store) logOperation(ctx context.Context, tx *sql.Tx, op OperationLog) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO memory_operations
			(operation,target_entry_id,replacement_entry_id,content_hash,reason,affected_count,created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		op.Operation, op.TargetEntryID, op.ReplacementEntryID, op.ContentHash,
		op.Reason, op.AffectedCount, op.CreatedAt)
	return err
}

func (s *Store) clusterIDsLocked(ctx context.Context, entryID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT cluster_id FROM memory_cluster_members WHERE entry_id=? ORDER BY cluster_id`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) activeSummaryCountLocked(ctx context.Context, clusters []string) int {
	if len(clusters) == 0 {
		return 0
	}
	args := make([]any, 0, len(clusters))
	for _, c := range clusters {
		args = append(args, c)
	}
	var n int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_summary_versions
		 WHERE status='active' AND cluster_id IN (`+placeholders(len(clusters))+`)`, args...).Scan(&n)
	return n
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
