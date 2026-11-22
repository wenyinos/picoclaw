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
	if err := s.checkReady(); err != nil {
		return err
	}

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
// superseded history with valid_to set, and the replacement, supersede edge and
// operation log commit in one transaction.
func (s *Store) Correct(ctx context.Context, id string, in AddInput) (*AddResult, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
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
	if strings.TrimSpace(in.DomainID) == "" {
		in.DomainID = old.DomainID
	}
	if strings.TrimSpace(in.Sensitivity) == "" {
		in.Sensitivity = old.Sensitivity
	}
	// A correction must not silently un-pin a core memory or change its
	// permission domain/sensitivity.
	in.PinnedCore = in.PinnedCore || old.PinnedCore
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
	if err := prepareAddInput(&in); err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	supersededID, pinnedBlockID, err := resolveSupersededID(ctx, tx, in, now)
	if err != nil {
		return nil, err
	}
	e, err := buildAndInsertEntryTx(ctx, tx, in, now, writeStatus(in, pinnedBlockID),
		supersededID, pinnedBlockID)
	if err != nil {
		return nil, err
	}
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "correct", TargetEntryID: id, ReplacementEntryID: e.ID,
		Reason: "user correction", AffectedCount: 1, CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	if err := s.invalidateSummariesForEntryTx(ctx, tx, id, "correct"); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &AddResult{Action: "superseded", Entry: e, ExistingID: id}, nil
}

// Forget hard-deletes a memory and its derived data. dryRun only reports the
// impact. It never deletes without a durable journal entry in the independent
// forget ledger: an accepted obligation must survive a restored backup.
//
// Relationship-state copies (nicknames, birthdays, moods) are removed with the
// entry: forgetting "call me X" must not leave the prompt block saying X.
func (s *Store) Forget(ctx context.Context, id string, dryRun bool) (*ForgetPreview, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var hash, status, text, domainID, predicate, subject string
	err := s.db.QueryRowContext(ctx,
		`SELECT content_hash,status,text,domain_id,predicate,subject FROM entries WHERE id=?`, id).
		Scan(&hash, &status, &text, &domainID, &predicate, &subject)
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
	relKeys, err := s.relationshipKeysForEntryLocked(ctx, id, predicate, subject)
	if err != nil {
		return nil, err
	}
	preview := &ForgetPreview{
		EntryID: id, ContentHash: hash, ClusterIDs: clusters, RelationshipKeys: relKeys,
	}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sources WHERE entry_id=?`, id).Scan(&preview.Sources)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_relations WHERE source_id=? OR target_id=?`, id, id).
		Scan(&preview.Relations)
	preview.Summaries = s.activeSummaryCountLocked(ctx, clusters)
	if dryRun {
		return preview, nil
	}

	// Journal first. Without the ledger the obligation could be lost by a
	// backup restore, so a destructive forget fails closed instead.
	if s.ledger == nil {
		return nil, fmt.Errorf("memstore: forget ledger unavailable; refusing destructive forget")
	}
	// Capture message locators while the sources still exist; after the delete
	// there is nothing left to point the cleanup worker at.
	locators, err := s.sourceLocatorsLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	manifest := ForgetTargetManifest{
		EntryIDs:         []string{id},
		RelationshipKeys: relKeys,
		CapsuleIDs:       clusters,
		Locators:         locators,
	}
	digest, keyID, hashVersion := s.ledger.Digest(text)
	intent, err := s.ledger.RecordIntent(ctx, ForgetIntent{
		DomainID:       domainID,
		SelectorType:   "entry+content_hmac",
		SelectorDigest: digest,
		HashVersion:    hashVersion,
		KeyID:          keyID,
		ScopeJSON:      `{"kind":"entry"}`,
		TargetManifest: MarshalManifest(manifest),
	})
	if err != nil {
		return nil, fmt.Errorf("memstore: forget journal: %w", err)
	}
	preview.LedgerSeq = intent.Seq
	preview.RequestID = intent.RequestID

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
		args := []any{now, now}
		for _, c := range clusters {
			args = append(args, c)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE memory_summary_versions SET status='invalidated', superseded_at=?,
			 invalidated_at=?
			 WHERE status='active' AND cluster_id IN (`+placeholders(len(clusters))+`)`,
			args...); err != nil {
			return nil, err
		}
	}
	// Relationship copies must go with the fact they were derived from.
	for _, key := range relKeys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM relationship_state WHERE key=?`, key); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM relationship_state_history WHERE key=?`, key); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE id=?`, id); err != nil {
		return nil, err
	}
	affected := preview.Sources + preview.Relations + preview.Summaries + len(relKeys) + 1
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "forget", TargetEntryID: id, ContentHash: hash,
		Reason: "user forget", AffectedCount: affected, CreatedAt: now,
		RequestID: intent.RequestID, ReasonCode: "user_request",
	}); err != nil {
		return nil, err
	}
	// The cross-store cleanup obligation is committed with the deletion: a
	// crash after commit still leaves a durable job pointing at the messages.
	if err := enqueueCleanupJobTx(ctx, tx, intent.RequestID, MarshalManifest(manifest)); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.ledger.MarkApplied(ctx, intent.Seq, s.revision.Load()); err != nil {
		// The obligation is journaled and the main DB is clean; a retry of
		// MarkApplied during recovery will finish the bookkeeping.
		return preview, nil
	}
	return preview, nil
}

// relationshipKeysForEntryLocked resolves which relationship-state keys are
// derived copies of one memory. Explicit source markers win; legacy rows are
// mapped by predicate so pre-v4 data is cleaned too.
func (s *Store) relationshipKeysForEntryLocked(ctx context.Context, entryID, predicate, subject string) ([]string, error) {
	keys := map[string]bool{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT key FROM relationship_state WHERE source_event_id=?`, "entry:"+entryID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			_ = rows.Close()
			return nil, err
		}
		keys[key] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, key := range relationshipKeysForPredicate(predicate, subject) {
		keys[key] = true
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	return out, nil
}

// relationshipKeysForPredicate maps extractor predicates to the relationship
// keys they mirror. subject narrows nickname copies to the right side.
func relationshipKeysForPredicate(predicate, subject string) []string {
	switch strings.TrimSpace(predicate) {
	case "nickname":
		switch strings.TrimSpace(subject) {
		case "user":
			return []string{"user_name"}
		case "ai":
			return []string{"ai_name"}
		default:
			return []string{"user_name", "ai_name"}
		}
	case "birthday":
		return []string{"birthday"}
	case "anniversary":
		return []string{"anniversary"}
	}
	return nil
}

// ReplayForget re-applies every forget obligation from the independent ledger
// to entries that came back after restoring an older backup. It matches by
// versioned HMAC, so a restored database never needs the deleted text.
func (s *Store) ReplayForget(ctx context.Context) (int, error) {
	if err := s.checkReady(); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ledger == nil {
		return 0, fmt.Errorf("memstore: forget ledger unavailable")
	}
	digests, err := s.ledger.AllDigests(ctx)
	if err != nil {
		return 0, err
	}
	if len(digests) == 0 {
		return 0, nil
	}
	digestSet := make(map[string]bool, len(digests))
	for _, d := range digests {
		digestSet[d] = true
	}

	// Relationship keys from journal manifests are needed because the entry
	// rows that would have pointed at them no longer exist.
	relKeys := map[string]bool{}
	intents, err := s.ledger.Recent(ctx, 500)
	if err != nil {
		return 0, err
	}
	for _, in := range intents {
		m, mErr := ParseManifest(in.TargetManifest)
		if mErr != nil {
			continue
		}
		for _, key := range m.RelationshipKeys {
			relKeys[key] = true
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// Collect ids first: the single SQLite connection cannot run Exec while a
	// result set is still open.
	type candidate struct {
		id   string
		text string
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, text FROM entries`)
	if err != nil {
		return 0, err
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.text); err != nil {
			_ = rows.Close()
			return 0, err
		}
		candidates = append(candidates, c)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	for _, c := range candidates {
		digest, _, _ := s.ledger.Digest(c.text)
		if !digestSet[digest] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM supersedes WHERE entry_id=? OR superseded_id=?`, c.id, c.id); err != nil {
			return total, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE id=?`, c.id); err != nil {
			return total, err
		}
		total++
	}
	for key := range relKeys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM relationship_state WHERE key=?`, key); err != nil {
			return total, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM relationship_state_history WHERE key=?`, key); err != nil {
			return total, err
		}
	}
	if total > 0 || len(relKeys) > 0 {
		if err := s.logOperation(ctx, tx, OperationLog{
			Operation: "replay_forget", Reason: "ledger replay", AffectedCount: total,
			CreatedAt: time.Now().UnixMilli(), ReasonCode: "recovery",
		}); err != nil {
			return total, err
		}
		if err := s.bumpRevision(ctx, tx); err != nil {
			return total, err
		}
	}
	if err := tx.Commit(); err != nil {
		return total, err
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
		        reason,affected_count,created_at,request_id,actor_ref,reason_code,policy_revision
		 FROM memory_operations ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationLog
	for rows.Next() {
		var op OperationLog
		if err := rows.Scan(&op.ID, &op.Operation, &op.TargetEntryID, &op.ReplacementEntryID,
			&op.ContentHash, &op.Reason, &op.AffectedCount, &op.CreatedAt,
			&op.RequestID, &op.ActorRef, &op.ReasonCode, &op.PolicyRevision); err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (s *Store) logOperation(ctx context.Context, tx *sql.Tx, op OperationLog) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO memory_operations
			(operation,target_entry_id,replacement_entry_id,content_hash,reason,affected_count,created_at,
			 request_id,actor_ref,reason_code,policy_revision)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		op.Operation, op.TargetEntryID, op.ReplacementEntryID, op.ContentHash,
		op.Reason, op.AffectedCount, op.CreatedAt,
		op.RequestID, op.ActorRef, op.ReasonCode, op.PolicyRevision)
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
