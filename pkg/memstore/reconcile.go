package memstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PendingReconcileReport summarizes a background reconciliation pass.
type PendingReconcileReport struct {
	Scanned            int `json:"scanned"`
	AppliedBookkeeping int `json:"applied_bookkeeping"`
	Replayed           int `json:"replayed"`
	Blocked            int `json:"blocked"`
	SkippedUnapproved  int `json:"skipped_unapproved"`
}

type pendingScope struct {
	Actor            string `json:"actor"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// ReconcilePendingIntents finishes approved forget intents that were accepted
// by the independent ledger but did not complete their main-database phase.
//
// Safety rules:
//   - a main operation with the same request_id proves the main phase committed:
//     only MarkApplied is retried;
//   - otherwise the intent must carry a trusted actor and expected_revision;
//     missing approval metadata is reported and left pending;
//   - a changed target revision blocks the intent instead of deleting new data.
func (s *Store) ReconcilePendingIntents(ctx context.Context, dryRun bool) (PendingReconcileReport, error) {
	report := PendingReconcileReport{}
	if err := s.checkReady(); err != nil {
		return report, err
	}
	if s.ledger == nil {
		return report, fmt.Errorf("memstore: forget ledger unavailable")
	}
	intents, err := s.ledger.Pending(ctx)
	if err != nil {
		return report, err
	}
	for _, intent := range intents {
		report.Scanned++
		var opID int64
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM memory_operations
			 WHERE request_id=? AND operation IN ('forget','replay_forget')
			 ORDER BY id DESC LIMIT 1`, intent.RequestID).Scan(&opID)
		if err == nil {
			if !dryRun {
				if err := s.ledger.MarkApplied(ctx, intent.Seq, s.revision.Load()); err != nil {
					return report, err
				}
			}
			report.AppliedBookkeeping++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}

		var scope pendingScope
		_ = json.Unmarshal([]byte(intent.ScopeJSON), &scope)
		if strings.TrimSpace(scope.Actor) == "" {
			// Legacy or unapproved pending intent: report only.
			report.SkippedUnapproved++
			continue
		}
		manifest, err := ParseManifest(intent.TargetManifest)
		if err != nil || len(manifest.EntryIDs) == 0 {
			report.Blocked++
			if !dryRun {
				_ = s.ledger.MarkBlocked(ctx, intent.Seq, "pending intent has no replayable manifest")
			}
			continue
		}
		clusters := map[string][]string{}
		for _, id := range manifest.EntryIDs {
			cs, err := s.clusterIDsLocked(ctx, id)
			if err != nil {
				return report, err
			}
			clusters[id] = cs
		}
		domainID := strings.TrimSpace(intent.DomainID)
		if domainID == "" {
			domainID = DomainLegacyUnassigned
		}

		// Verify every target revision before touching anything.
		conflict := false
		for _, id := range manifest.EntryIDs {
			var revision int64
			var rowDomain string
			err := s.db.QueryRowContext(ctx,
				`SELECT row_revision,domain_id FROM entries WHERE id=?`, id).
				Scan(&revision, &rowDomain)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return report, err
			}
			if rowDomain != domainID ||
				(scope.ExpectedRevision > 0 && revision != scope.ExpectedRevision) {
				conflict = true
				break
			}
		}
		if conflict {
			report.Blocked++
			if !dryRun {
				_ = s.ledger.MarkBlocked(ctx, intent.Seq, "target changed since approval")
			}
			continue
		}
		if dryRun {
			report.Replayed++
			continue
		}

		s.mu.Lock()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			s.mu.Unlock()
			return report, err
		}
		now := s.now()
		affected := 0
		for _, id := range manifest.EntryIDs {
			if err := s.redactSummariesForEntryTx(ctx, tx, id, clusters[id], now); err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM supersedes WHERE entry_id=? OR superseded_id=?`, id, id); err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM relationship_state WHERE source_event_id=?`, "entry:"+id); err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM relationship_state_history WHERE source_event_id=?`, "entry:"+id); err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
			res, err := tx.ExecContext(ctx,
				`DELETE FROM entries WHERE id=? AND domain_id=?`, id, domainID)
			if err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				affected += int(n)
			}
		}
		if affected > 0 {
			if err := s.logOperation(ctx, tx, OperationLog{
				Operation: "replay_forget", TargetEntryID: strings.Join(manifest.EntryIDs, ","),
				Reason: "approved pending intent reconciliation", AffectedCount: affected,
				CreatedAt: now, RequestID: intent.RequestID, ActorRef: scope.Actor,
				ReasonCode: "recovery",
			}); err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
		}
		if err := enqueueCleanupJobTx(ctx, tx, intent.RequestID, MarshalManifest(manifest)); err != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return report, err
		}
		if affected > 0 {
			if err := s.bumpRevision(ctx, tx); err != nil {
				_ = tx.Rollback()
				s.mu.Unlock()
				return report, err
			}
		}
		if err := tx.Commit(); err != nil {
			s.mu.Unlock()
			return report, err
		}
		s.mu.Unlock()

		if err := s.ledger.MarkApplied(ctx, intent.Seq, s.revision.Load()); err != nil {
			return report, err
		}
		report.Replayed++
	}
	return report, nil
}
