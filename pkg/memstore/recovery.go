package memstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrMemoryNotReady is returned when an accepted deletion obligation or schema
// inconsistency must be resolved before long-term memory can be used.
var ErrMemoryNotReady = errors.New("memstore: long-term memory is waiting for recovery")

// RecoveryStatus is the startup/readiness account for long-term memory. It
// contains counts and reason codes only.
type RecoveryStatus struct {
	Ready            bool     `json:"ready"`
	SchemaReady      bool     `json:"schema_ready"`
	LedgerReady      bool     `json:"ledger_ready"`
	DomainRegistryOK bool     `json:"domain_registry_ok"`
	PendingIntents   int      `json:"pending_intents"`
	BlockedIntents   int      `json:"blocked_intents"`
	BlockedCleanup   int      `json:"blocked_cleanup"`
	InertCleanup     int      `json:"inert_cleanup"`
	PendingCleanup   int      `json:"pending_cleanup"`
	RunningCleanup   int      `json:"running_cleanup"`
	OrphanEntries    int      `json:"orphan_entries"`
	Issues           []string `json:"issues,omitempty"`
	CheckedAt        int64    `json:"checked_at"`
}

// RecoveryPreflight runs the safe recovery phase before an agent starts
// serving memory: approved pending intents are reconciled, then the store is
// checked for conditions that must fail closed.
func (s *Store) RecoveryPreflight(ctx context.Context) (RecoveryStatus, error) {
	var status RecoveryStatus
	if s.ledger != nil {
		report, err := s.ReconcilePendingIntents(ctx, false)
		if err != nil {
			return status, fmt.Errorf("memstore: recovery reconcile: %w", err)
		}
		status.PendingIntents = report.SkippedUnapproved
	}
	status, err := s.RecoveryStatus(ctx)
	return status, err
}

// RecoveryStatus evaluates readiness without mutating either database.
func (s *Store) RecoveryStatus(ctx context.Context) (RecoveryStatus, error) {
	status := RecoveryStatus{SchemaReady: true, LedgerReady: s.ledger != nil,
		DomainRegistryOK: true, CheckedAt: time.Now().UnixMilli()}
	if err := s.checkReady(); err != nil {
		status.SchemaReady = false
		status.Issues = append(status.Issues, "schema_not_ready")
	} else if s.PendingMigration() {
		status.SchemaReady = false
		status.Issues = append(status.Issues, "schema_migration_required")
	}
	if !status.LedgerReady {
		status.Issues = append(status.Issues, "forget_ledger_unavailable")
	}

	if status.SchemaReady {
		var domainRow int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM memory_domains WHERE domain_id=?`,
			DomainLegacyUnassigned).Scan(&domainRow); err != nil {
			return status, err
		}
		if domainRow == 0 {
			status.DomainRegistryOK = false
			status.Issues = append(status.Issues, "default_domain_missing")
		}
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM entries e
			 LEFT JOIN memory_domains d ON d.domain_id=e.domain_id
			 WHERE d.domain_id IS NULL`).Scan(&status.OrphanEntries); err != nil {
			return status, err
		}
		if status.OrphanEntries > 0 {
			status.DomainRegistryOK = false
			status.Issues = append(status.Issues, "entries_without_domain")
		}
	}

	if status.LedgerReady {
		rows, err := s.ledger.db.QueryContext(ctx,
			`SELECT status, COUNT(*) FROM forget_journal
			 WHERE status IN ('pending','blocked') GROUP BY status`)
		if err != nil {
			return status, err
		}
		for rows.Next() {
			var state string
			var count int
			if err := rows.Scan(&state, &count); err != nil {
				_ = rows.Close()
				return status, err
			}
			switch state {
			case "pending":
				status.PendingIntents += count
			case "blocked":
				status.BlockedIntents += count
			}
		}
		if err := rows.Close(); err != nil {
			return status, err
		}
		if status.PendingIntents > 0 {
			status.Issues = append(status.Issues, "pending_forget_intents")
		}
		if status.BlockedIntents > 0 {
			status.Issues = append(status.Issues, "blocked_forget_intents")
		}
	}

	if status.SchemaReady {
		rows, err := s.db.QueryContext(ctx,
			`SELECT state, COUNT(*) FROM memory_jobs
			 WHERE job_kind='forget_cleanup'
			   AND state IN ('pending','running','blocked','cancelled')
			 GROUP BY state`)
		if err != nil {
			return status, err
		}
		for rows.Next() {
			var state string
			var count int
			if err := rows.Scan(&state, &count); err != nil {
				_ = rows.Close()
				return status, err
			}
			switch state {
			case JobPending:
				status.PendingCleanup += count
			case JobRunning:
				status.RunningCleanup += count
			case JobBlocked:
				status.BlockedCleanup += count
			case JobCancelled:
				status.InertCleanup += count
			}
		}
		if err := rows.Close(); err != nil {
			return status, err
		}
		if status.PendingCleanup > 0 {
			status.Issues = append(status.Issues, "pending_cleanup_jobs")
		}
		if status.RunningCleanup > 0 {
			status.Issues = append(status.Issues, "running_cleanup_jobs")
		}
		if status.BlockedCleanup > 0 {
			status.Issues = append(status.Issues, "blocked_cleanup_jobs")
		}
		if status.InertCleanup > 0 {
			status.Issues = append(status.Issues, "cancelled_cleanup_jobs")
		}
	}

	status.Ready = status.SchemaReady && status.LedgerReady && status.DomainRegistryOK &&
		status.PendingIntents == 0 && status.BlockedIntents == 0 &&
		status.PendingCleanup == 0 && status.RunningCleanup == 0 &&
		status.BlockedCleanup == 0 && status.InertCleanup == 0
	return status, nil
}

// RequireOperational blocks normal memory reads and writes until all accepted
// deletion obligations and domain invariants are resolved. Management paths use
// Store methods directly so the owner can still inspect and repair the state.
func (s *Store) RequireOperational(ctx context.Context) error {
	status, err := s.RecoveryStatus(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMemoryNotReady, err)
	}
	if !status.Ready {
		issues := strings.Join(status.Issues, ",")
		return fmt.Errorf("%w (%s)", ErrMemoryNotReady, issues)
	}
	return nil
}
