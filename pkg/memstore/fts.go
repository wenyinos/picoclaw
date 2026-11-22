package memstore

import (
	"context"
	"strings"
	"unicode/utf8"
)

// ftsHit is one full-text search result. Rank is already positive: higher is
// more relevant (bm25 returns negative values).
type ftsHit struct {
	EntryID string
	Rank    float64
}

// buildFTSQuery splits a query into a MATCH phrase and LIKE terms. FTS5
// trigram needs at least three runes, so one- and two-character Chinese
// queries (生日, 海边, 同意) must go through LIKE instead of silently
// returning zero hits. Long queries keep a LIKE fallback too: when the FTS
// index is missing or recall is thin, an exact-phrase LIKE scan still finds
// the memory instead of returning nothing (B09).
func buildFTSQuery(q string) (string, []string) {
	q = strings.TrimSpace(strings.NewReplacer(`"`, " ", `'`, " ").Replace(q))
	n := utf8.RuneCountInString(q)
	if n == 0 {
		return "", nil
	}
	if n < 3 {
		return "", []string{q}
	}
	// Cap the fallback term so a pasted paragraph does not turn into a
	// pathological LIKE scan; short long-queries (e.g. a full sentence) keep
	// the entire phrase.
	termLimit := 40
	term := q
	if n > termLimit {
		term = string([]rune(q)[:termLimit])
	}
	return `"` + q + `"`, []string{term}
}

func (s *Store) ftsTableExists(ctx context.Context) bool {
	return s.tableExistsLocked(ctx, "entries_fts")
}

// searchFTS runs one MATCH query and returns rowid-aligned hits. filter is an
// extra SQL predicate applied inside the query, so LIMIT counts visible rows.
func (s *Store) searchFTS(ctx context.Context, query string, limit int, filter string, filterArgs []any) ([]ftsHit, error) {
	match, _ := buildFTSQuery(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	where := `entries_fts MATCH ? AND e.status='active'`
	if strings.TrimSpace(filter) != "" {
		where += ` AND ` + filter
	}
	args := append([]any{match}, filterArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, bm25(entries_fts) AS rank
		 FROM entries_fts f
		 JOIN entries e ON e.rowid = f.rowid
		 WHERE `+where+`
		 ORDER BY bm25(entries_fts) LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ftsHit
	for rows.Next() {
		var hit ftsHit
		var bm25 float64
		if err := rows.Scan(&hit.EntryID, &bm25); err != nil {
			return nil, err
		}
		hit.Rank = -bm25
		out = append(out, hit)
	}
	return out, rows.Err()
}

// searchLike is the fallback channel for short queries and for FTS builds
// where the tokenizer degrades to whole-word matching. filter is applied in
// SQL so the LIMIT cannot be consumed by invisible rows (B07).
func (s *Store) searchLike(ctx context.Context, term string, limit int, filter string, filterArgs []any) ([]string, error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	like := "%" + term + "%"
	where := `e.status='active'
		   AND (e.text LIKE ? OR e.value LIKE ? OR e.subject LIKE ? OR e.predicate LIKE ? OR e.facet LIKE ?)`
	if strings.TrimSpace(filter) != "" {
		where += ` AND ` + filter
	}
	args := []any{like, like, like, like, like}
	args = append(args, filterArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id FROM entries e
		 WHERE `+where+`
		 ORDER BY e.importance DESC, e.updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RebuildFTS repopulates the FTS index from entries. It is safe to run while
// the service is running and returns the number of indexed rows.
func (s *Store) RebuildFTS(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ftsTableExists(ctx) {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries_fts`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO entries_fts(rowid,subject,predicate,facet,value,text)
		 SELECT rowid,subject,predicate,facet,value,text FROM entries`); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&n); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// FTSCount returns the number of indexed rows.
func (s *Store) FTSCount(ctx context.Context) (int, error) {
	if !s.ftsTableExists(ctx) {
		return 0, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&n)
	return n, err
}
