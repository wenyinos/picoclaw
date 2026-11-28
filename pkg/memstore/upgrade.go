package memstore

import (
	"context"
	"fmt"
	"strings"
)

// UpgradeStep describes one explicit action performed by the one-time upgrade
// wizard.
type UpgradeStep struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// UpgradeCleanupJob is a pre-upgrade cleanup obligation. The wizard does not
// expose payload locators or memory text.
type UpgradeCleanupJob struct {
	JobID    string `json:"job_id"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
}

// UpgradePreview explains exactly what the one-time wizard will change.
type UpgradePreview struct {
	FromSchemaVersion    int                 `json:"from_schema_version"`
	TargetSchemaVersion  int                 `json:"target_schema_version"`
	SchemaReady          bool                `json:"schema_ready"`
	LedgerReady          bool                `json:"ledger_ready"`
	DefaultDomainMissing bool                `json:"default_domain_missing"`
	OrphanEntries        int                 `json:"orphan_entries"`
	LegacyCleanupJobs    []UpgradeCleanupJob `json:"legacy_cleanup_jobs,omitempty"`
	PendingCleanup       int                 `json:"pending_cleanup"`
	RunningCleanup       int                 `json:"running_cleanup"`
	PendingIntents       int                 `json:"pending_intents"`
	BlockedIntents       int                 `json:"blocked_intents"`
	Steps                []UpgradeStep       `json:"steps"`
	BlockingReasons      []string            `json:"blocking_reasons,omitempty"`
	CanComplete          bool                `json:"can_complete"`
	Recovery             RecoveryStatus      `json:"recovery"`
}

// UpgradeReport is returned after the explicit one-time upgrade. It never
// claims that legacy unverifiable copies were deleted.
type UpgradeReport struct {
	PreviousSchemaVersion int            `json:"previous_schema_version"`
	SchemaVersion         int            `json:"schema_version"`
	Backup                string         `json:"backup"`
	ClosedLegacyJobs      int            `json:"closed_legacy_jobs"`
	ClosedJobIDs          []string       `json:"closed_job_ids,omitempty"`
	ResidualRisk          bool           `json:"residual_risk"`
	ResidualMessage       string         `json:"residual_message,omitempty"`
	Ready                 bool           `json:"ready"`
	Recovery              RecoveryStatus `json:"recovery"`
}

// PreviewUpgrade is read-only. It detects whether all current recovery blockers
// are in the migratable set and whether any deletion obligation must remain
// unverified.
func (s *Store) PreviewUpgrade(ctx context.Context) (UpgradePreview, error) {
	migration, err := s.MigrationStatus(ctx)
	if err != nil {
		return UpgradePreview{}, err
	}
	recovery, err := s.RecoveryStatus(ctx)
	if err != nil {
		return UpgradePreview{}, err
	}
	preview := UpgradePreview{
		FromSchemaVersion:   migration.SchemaVersion,
		TargetSchemaVersion: CurrentSchemaVersion,
		SchemaReady:         recovery.SchemaReady,
		LedgerReady:         recovery.LedgerReady,
		OrphanEntries:       recovery.OrphanEntries,
		PendingCleanup:      recovery.PendingCleanup,
		RunningCleanup:      recovery.RunningCleanup,
		PendingIntents:      recovery.PendingIntents,
		BlockedIntents:      recovery.BlockedIntents,
		Recovery:            recovery,
	}
	preview.DefaultDomainMissing, err = s.defaultDomainMissing(ctx)
	if err != nil {
		return UpgradePreview{}, err
	}
	jobs, err := s.legacyCleanupJobs(ctx)
	if err != nil {
		return UpgradePreview{}, err
	}
	preview.LegacyCleanupJobs = jobs

	if migration.SchemaVersion < MinMigratableSchemaVersion {
		preview.BlockingReasons = append(preview.BlockingReasons, "unsupported_schema")
	}
	if migration.SchemaVersion > CurrentSchemaVersion {
		preview.BlockingReasons = append(preview.BlockingReasons, "future_schema")
	}
	if !recovery.LedgerReady {
		preview.BlockingReasons = append(preview.BlockingReasons, "ledger_unavailable")
	}
	if recovery.PendingCleanup > 0 {
		preview.BlockingReasons = append(preview.BlockingReasons, "pending_cleanup_jobs")
	}
	if recovery.RunningCleanup > 0 {
		preview.BlockingReasons = append(preview.BlockingReasons, "running_cleanup_jobs")
	}
	if recovery.PendingIntents > 0 {
		preview.BlockingReasons = append(preview.BlockingReasons, "pending_forget_intents")
	}
	if recovery.BlockedIntents > 0 {
		preview.BlockingReasons = append(preview.BlockingReasons, "blocked_forget_intents")
	}

	if migration.NeedsSchemaMigration || migration.SchemaVersion < CurrentSchemaVersion {
		preview.Steps = append(preview.Steps, UpgradeStep{
			Code: "schema", Title: "Migrate schema to v4",
			Detail: "Back up the database and ledger, then apply the explicit v3 to v4 migration.",
		})
	}
	if preview.DefaultDomainMissing || preview.OrphanEntries > 0 {
		preview.Steps = append(preview.Steps, UpgradeStep{
			Code: "default_domain", Title: "Restore the legacy default domain",
			Detail: "Back up first, then insert only the known legacy_unassigned domain row.",
		})
	}
	if len(jobs) > 0 {
		preview.Steps = append(preview.Steps, UpgradeStep{
			Code: "legacy_cleanup", Title: "Close pre-upgrade cleanup obligations",
			Detail: "Mark cancelled, blocked or failed legacy cleanup jobs as legacy_closed with an audit record. This does not claim their unverifiable copies were deleted.",
		})
	}
	preview.Steps = append(preview.Steps, UpgradeStep{
		Code: "verify", Title: "Recheck recovery readiness",
		Detail: "Recompute domain, ledger, intent and cleanup gates after the one-time changes.",
	})
	preview.CanComplete = len(preview.BlockingReasons) == 0
	return preview, nil
}

// ApplyUpgrade performs the explicit one-time maintenance sequence:
// repair schema/domain, take a fresh backup, close legacy cleanup obligations
// with audit, then recompute readiness. Legacy closure is not reported as
// verified deletion.
func (s *Store) ApplyUpgrade(ctx context.Context) (UpgradeReport, error) {
	preview, err := s.PreviewUpgrade(ctx)
	if err != nil {
		return UpgradeReport{}, err
	}
	if !preview.CanComplete {
		return UpgradeReport{}, fmt.Errorf(
			"memstore: one-time upgrade cannot complete: %s",
			strings.Join(preview.BlockingReasons, ","))
	}
	repair, err := s.RepairData(ctx)
	if err != nil {
		return UpgradeReport{}, fmt.Errorf("memstore: upgrade schema/domain repair: %w", err)
	}
	// Always snapshot the repaired database before changing historical cleanup
	// states, even when RepairData itself did not need a backup.
	backup, err := s.backupWithRotation(ctx, "upgrade-v0.7.26")
	if err != nil {
		return UpgradeReport{}, fmt.Errorf("memstore: upgrade backup: %w", err)
	}
	closedIDs, err := s.closeLegacyCleanupJobs(ctx)
	if err != nil {
		return UpgradeReport{}, fmt.Errorf("memstore: close legacy cleanup: %w", err)
	}
	recovery, err := s.RecoveryStatus(ctx)
	if err != nil {
		return UpgradeReport{}, err
	}
	report := UpgradeReport{
		PreviousSchemaVersion: repair.PreviousSchemaVersion,
		SchemaVersion:         repair.SchemaVersion,
		Backup:                backup,
		ClosedLegacyJobs:      len(closedIDs),
		ClosedJobIDs:          closedIDs,
		Ready:                 recovery.Ready,
		Recovery:              recovery,
	}
	if len(closedIDs) > 0 {
		report.ResidualRisk = true
		report.ResidualMessage = "Legacy cleanup jobs were closed as unverified; their historical source copies may remain. They no longer block long-term memory, but this is not a verified deletion."
	}
	if !recovery.Ready {
		return report, fmt.Errorf("memstore: upgrade completed but recovery is still blocked: %s",
			strings.Join(recovery.Issues, ","))
	}
	return report, nil
}

func (s *Store) defaultDomainMissing(ctx context.Context) (bool, error) {
	var tableRows int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memory_domains'`).
		Scan(&tableRows); err != nil {
		return false, err
	}
	if tableRows == 0 {
		return true, nil
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_domains WHERE domain_id=?`,
		DomainLegacyUnassigned).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func (s *Store) legacyCleanupJobs(ctx context.Context) ([]UpgradeCleanupJob, error) {
	var tableRows int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memory_jobs'`).
		Scan(&tableRows); err != nil {
		return nil, err
	}
	if tableRows == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT job_id,state,attempts FROM memory_jobs
		 WHERE job_kind='forget_cleanup' AND state IN ('cancelled','blocked','failed')
		 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UpgradeCleanupJob{}
	for rows.Next() {
		var job UpgradeCleanupJob
		if err := rows.Scan(&job.JobID, &job.State, &job.Attempts); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) closeLegacyCleanupJobs(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx,
		`SELECT job_id FROM memory_jobs
		 WHERE job_kind='forget_cleanup' AND state IN ('cancelled','blocked','failed')
		 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	now := s.now()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE memory_jobs
			 SET state='legacy_closed', lease_owner='', lease_until=NULL,
			     last_error='pre-upgrade cleanup closed as unverified; copies may remain',
			     updated_at=?
			 WHERE job_id=? AND state IN ('cancelled','blocked','failed')`,
			now, id); err != nil {
			return nil, err
		}
	}
	if err := s.logOperation(ctx, tx, OperationLog{
		Operation: "legacy_cleanup_close", TargetEntryID: "upgrade:0.7.26",
		Reason:        "one-time upgrade closed unverifiable legacy cleanup jobs",
		AffectedCount: len(ids), CreatedAt: now,
		RequestID: auditRequest(ctx), ReasonCode: "legacy_cleanup_unverified",
	}); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
