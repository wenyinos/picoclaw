package memstore

import (
	"context"
	"database/sql"
	"errors"
)

// ForgetAuditItem is metadata-only: it never includes memory text.
type ForgetAuditItem struct {
	OperationID  int64  `json:"operation_id"`
	TargetID     string `json:"target_entry_id"`
	ContentHash  string `json:"content_hash"`
	CreatedAt    int64  `json:"created_at"`
	TargetExists bool   `json:"target_exists"`
	DomainID     string `json:"domain_id,omitempty"`
	RowRevision  int64  `json:"row_revision,omitempty"`
}

// ForgetAuditReport describes historical forget operations. It is report-only:
// content_hash has no domain and cannot be converted into a ledger selector, so
// no automatic deletion is derived from this data.
type ForgetAuditReport struct {
	Total          int               `json:"total"`
	TargetsPresent int               `json:"targets_present"`
	TargetsGone    int               `json:"targets_gone"`
	MissingTarget  int               `json:"missing_target_id"`
	NonLegacy      int               `json:"targets_non_legacy"`
	Items          []ForgetAuditItem `json:"items"`
}

// AuditHistoricalForgets scans memory_operations without mutating data. A
// target still being present means a restore or later write recreated the ID;
// an operator must review it explicitly before any new deletion.
func (s *Store) AuditHistoricalForgets(ctx context.Context, limit int) (ForgetAuditReport, error) {
	if err := s.checkReady(); err != nil {
		return ForgetAuditReport{}, err
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,target_entry_id,content_hash,created_at
		 FROM memory_operations WHERE operation='forget'
		 ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return ForgetAuditReport{}, err
	}
	var items []ForgetAuditItem
	for rows.Next() {
		var item ForgetAuditItem
		if err := rows.Scan(&item.OperationID, &item.TargetID, &item.ContentHash, &item.CreatedAt); err != nil {
			_ = rows.Close()
			return ForgetAuditReport{}, err
		}
		items = append(items, item)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return ForgetAuditReport{}, err
	}
	report := ForgetAuditReport{Items: items}
	for i := range report.Items {
		item := &report.Items[i]
		report.Total++
		if item.TargetID == "" {
			report.MissingTarget++
			continue
		}
		var domainID string
		var revision int64
		err := s.db.QueryRowContext(ctx,
			`SELECT domain_id,row_revision FROM entries WHERE id=?`, item.TargetID).
			Scan(&domainID, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			report.TargetsGone++
			continue
		}
		if err != nil {
			return ForgetAuditReport{}, err
		}
		item.TargetExists = true
		item.DomainID = domainID
		item.RowRevision = revision
		report.TargetsPresent++
		if domainID != DomainLegacyUnassigned {
			report.NonLegacy++
		}
	}
	return report, nil
}
