package memstore

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

func queryRelevance(queryNorm, textNorm string) float64 {
	if queryNorm == "" || textNorm == "" {
		return 0
	}
	if strings.Contains(textNorm, queryNorm) {
		return 1
	}
	return Jaccard(queryNorm, textNorm)
}

// SearchEntries is the administrative/tool search path. Unlike Search, it is
// not limited to episodic/procedural memories and does not apply audience
// filtering unless the caller explicitly asks for an audience. This lets the
// memory tool find profile, relationship, boundary, and private memories.
func (s *Store) SearchEntries(ctx context.Context, opts SearchOptions) ([]Entry, error) {
	where := []string{"1=1"}
	args := []any{}

	if opts.Realm != "" {
		where = append(where, "realm=?")
		args = append(args, opts.Realm)
	}
	if opts.Layer != "" {
		where = append(where, "layer=?")
		args = append(args, opts.Layer)
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = StatusActive
	}
	if !strings.EqualFold(status, "all") {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if opts.Audience != "" {
		where = append(where, "audience=?")
		args = append(args, opts.Audience)
	}
	if opts.Scope != "" {
		where = append(where, "scope=?")
		args = append(args, opts.Scope)
	}
	if opts.Predicate != "" {
		where = append(where, "predicate=?")
		args = append(args, opts.Predicate)
	}
	if opts.Facet != "" {
		where = append(where, "facet=?")
		args = append(args, opts.Facet)
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	queryNorm := Normalize(opts.Query)
	sql := `SELECT ` + entryColumns + ` FROM entries WHERE ` + strings.Join(where, " AND ")
	if queryNorm == "" {
		sql += ` ORDER BY importance DESC, updated_at DESC LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UnixMilli()
	scored := make([]scoredEntry, 0)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		recency := recencyScore(e.UpdatedAt, now)
		if queryNorm == "" {
			scored = append(scored, scoredEntry{
				entry: e,
				score: e.Importance*0.6 + recency*0.2 + e.EmotionalWeight*0.2,
			})
			continue
		}

		similarity := queryRelevance(queryNorm, e.Normalized)
		if similarity <= 0 {
			similarity = queryRelevance(queryNorm, Normalize(e.Predicate+" "+e.Facet))
		}
		if similarity <= 0 {
			continue
		}
		scored = append(scored, scoredEntry{
			entry: e,
			score: similarity*0.6 + e.Importance*0.2 + recency*0.1 + e.EmotionalWeight*0.1,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if queryNorm != "" {
		sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
		if offset >= len(scored) {
			return []Entry{}, nil
		}
		scored = scored[offset:]
		if len(scored) > limit {
			scored = scored[:limit]
		}
	}

	out := make([]Entry, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.entry)
	}
	return out, nil
}

func recencyScore(updatedAt, now int64) float64 {
	age := now - updatedAt
	if age <= 0 {
		return 1
	}
	recency := 1 - float64(age)/(1000*60*60*24*30)
	if recency < 0 {
		return 0
	}
	return recency
}

// RepairFilter selects entries for a bulk migration. Empty fields mean "any".
type RepairFilter struct {
	Realm    string `json:"realm"`
	Layer    string `json:"layer"`
	Status   string `json:"status"`
	Audience string `json:"audience"`
	Query    string `json:"query"`
}

// RepairTarget is the new classification for matched entries.
type RepairTarget struct {
	Realm    string `json:"realm"`
	Layer    string `json:"layer"`
	Audience string `json:"audience"`
}

// RepairPreview reports how many entries match a migration and returns a small
// sample for confirmation.
type RepairPreview struct {
	Count   int     `json:"count"`
	Entries []Entry `json:"entries"`
}

func repairWhere(filter RepairFilter) (string, []any) {
	where := []string{"1=1"}
	args := []any{}
	if filter.Realm != "" {
		where = append(where, "realm=?")
		args = append(args, filter.Realm)
	}
	if filter.Layer != "" {
		where = append(where, "layer=?")
		args = append(args, filter.Layer)
	}
	if filter.Status != "" && !strings.EqualFold(filter.Status, "all") {
		where = append(where, "status=?")
		args = append(args, filter.Status)
	}
	if filter.Audience != "" {
		where = append(where, "audience=?")
		args = append(args, filter.Audience)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		where = append(where, "(text LIKE ? OR value LIKE ? OR subject LIKE ? OR predicate LIKE ? OR facet LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like, like, like, like)
	}
	return strings.Join(where, " AND "), args
}

func validateRepairTarget(target RepairTarget) error {
	switch target.Realm {
	case RealmReal, RealmFictional, RealmWork, RealmUnknown:
	default:
		return fmt.Errorf("invalid target realm %q", target.Realm)
	}
	switch target.Layer {
	case LayerPersona, LayerProfile, LayerRelationship, LayerEpisodic, LayerProcedural, LayerBoundary:
	default:
		return fmt.Errorf("invalid target layer %q", target.Layer)
	}
	if target.Audience != "" && target.Audience != "all" && target.Audience != "private" && !strings.HasPrefix(target.Audience, "channel:") {
		return fmt.Errorf("invalid target audience %q", target.Audience)
	}
	return nil
}

// PreviewRepair counts matching entries and returns up to 20 examples.
func (s *Store) PreviewRepair(ctx context.Context, filter RepairFilter) (RepairPreview, error) {
	where, args := repairWhere(filter)
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE `+where, args...).Scan(&count); err != nil {
		return RepairPreview{}, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE `+where+` ORDER BY importance DESC, updated_at DESC LIMIT 20`, args...)
	if err != nil {
		return RepairPreview{}, err
	}
	defer rows.Close()
	var entries []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return RepairPreview{}, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return RepairPreview{}, err
	}
	return RepairPreview{Count: count, Entries: entries}, nil
}

// ApplyRepair creates a consistent backup and then updates the classification
// of matching entries in one transaction.
func (s *Store) ApplyRepair(ctx context.Context, filter RepairFilter, target RepairTarget) (int, string, error) {
	if err := validateRepairTarget(target); err != nil {
		return 0, "", err
	}
	if filter.Realm == "" && filter.Layer == "" && filter.Audience == "" && strings.TrimSpace(filter.Query) == "" {
		return 0, "", fmt.Errorf("refusing to reclassify without a realm, layer, audience, or query filter")
	}
	where, args := repairWhere(filter)

	s.mu.Lock()
	defer s.mu.Unlock()

	backupPath := s.path + ".backup-" + time.Now().Format("20060102-150405")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, backupPath); err != nil {
		return 0, "", fmt.Errorf("backup before repair: %w", err)
	}
	_ = os.Chmod(backupPath, 0o600)

	set := "realm=?, layer=?, updated_at=?"
	updateArgs := []any{target.Realm, target.Layer, time.Now().UnixMilli()}
	if target.Audience != "" {
		set = "realm=?, layer=?, audience=?, updated_at=?"
		updateArgs = []any{target.Realm, target.Layer, target.Audience, time.Now().UnixMilli()}
	}
	updateArgs = append(updateArgs, args...)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, backupPath, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE entries SET `+set+` WHERE `+where, updateArgs...)
	if err != nil {
		return 0, backupPath, err
	}
	n, _ := res.RowsAffected()
	if err := s.bumpRevision(ctx, tx); err != nil {
		return 0, backupPath, err
	}
	if err := tx.Commit(); err != nil {
		return 0, backupPath, err
	}
	return int(n), backupPath, nil
}
