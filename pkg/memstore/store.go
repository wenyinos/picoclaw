package memstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed long-term memory store.
type Store struct {
	db       *sql.DB
	path     string
	mu       sync.Mutex
	revision atomic.Int64
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS entries (
		id TEXT PRIMARY KEY,
		realm TEXT NOT NULL,
		layer TEXT NOT NULL,
		scope TEXT NOT NULL DEFAULT 'user',
		audience TEXT NOT NULL DEFAULT 'all',
		subject TEXT NOT NULL DEFAULT '',
		predicate TEXT NOT NULL DEFAULT '',
		value TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL,
		normalized TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		simhash INTEGER NOT NULL DEFAULT 0,
		template_key TEXT NOT NULL DEFAULT '',
		cluster_id TEXT NOT NULL DEFAULT '',
		occurrences INTEGER NOT NULL DEFAULT 1,
		importance REAL NOT NULL DEFAULT 0.5,
		emotional_weight REAL NOT NULL DEFAULT 0,
		valence REAL NOT NULL DEFAULT 0,
		confidence REAL NOT NULL DEFAULT 0.5,
		status TEXT NOT NULL DEFAULT 'active',
		event_at INTEGER,
		expires_at INTEGER,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		last_used_at INTEGER,
		use_count INTEGER NOT NULL DEFAULT 0,
		attributes TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_entries_hash ON entries(content_hash) WHERE status='active'`,
	`CREATE INDEX IF NOT EXISTS ix_entries_key ON entries(realm, subject, predicate, status)`,
	`CREATE INDEX IF NOT EXISTS ix_entries_inject ON entries(status, realm, layer, importance DESC)`,
	`CREATE INDEX IF NOT EXISTS ix_entries_expire ON entries(status, expires_at)`,
	`CREATE INDEX IF NOT EXISTS ix_entries_event ON entries(layer, event_at DESC)`,
	`CREATE TABLE IF NOT EXISTS sources (
		entry_id TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_id TEXT NOT NULL DEFAULT '',
		session_key TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		PRIMARY KEY (entry_id, source_type, source_id, session_key)
	)`,
	`CREATE TABLE IF NOT EXISTS supersedes (
		entry_id TEXT NOT NULL,
		superseded_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (entry_id, superseded_id)
	)`,
	`CREATE TABLE IF NOT EXISTS relationship_state (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		valid_from INTEGER NOT NULL,
		valid_to INTEGER,
		confidence REAL NOT NULL DEFAULT 0.8,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS personas (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		version INTEGER NOT NULL,
		content TEXT NOT NULL,
		active INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	)`,
}

// Open opens or creates the SQLite store at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("memstore: create dir: %w", err)
	}
	dsn := path
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memstore: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memstore: %s: %w", pragma, err)
		}
	}
	for _, stmt := range schemaStatements {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memstore: schema: %w", err)
		}
	}
	s := &Store{db: db, path: path}
	if err := s.loadRevision(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) loadRevision() error {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='revision'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES('revision','0'),('schema_version','1')`); err != nil {
			return fmt.Errorf("memstore: init meta: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("memstore: read revision: %w", err)
	}
	var rev int64
	fmt.Sscanf(raw, "%d", &rev)
	s.revision.Store(rev)
	return nil
}

// Close checkpoints the WAL and closes the database. Checkpointing keeps the
// workspace portable: only memory.db needs to be copied.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Revision changes on every write and is used for prompt cache invalidation.
func (s *Store) Revision() int64 {
	if s == nil {
		return 0
	}
	return s.revision.Load()
}

func (s *Store) bumpRevision(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='revision'`); err != nil {
		return err
	}
	s.revision.Add(1)
	return nil
}

var entryColumns = `id, realm, layer, scope, audience, subject, predicate, value, text,
	normalized, content_hash, simhash, template_key, cluster_id, occurrences,
	importance, emotional_weight, valence, confidence, status, event_at, expires_at,
	created_at, updated_at, last_used_at, use_count, attributes`

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row scanner) (Entry, error) {
	var e Entry
	var simhash int64
	var eventAt, expiresAt, lastUsed sql.NullInt64
	var attrs string
	err := row.Scan(
		&e.ID, &e.Realm, &e.Layer, &e.Scope, &e.Audience, &e.Subject, &e.Predicate,
		&e.Value, &e.Text, &e.Normalized, &e.ContentHash, &simhash, &e.TemplateKey,
		&e.ClusterID, &e.Occurrences, &e.Importance, &e.EmotionalWeight, &e.Valence,
		&e.Confidence, &e.Status, &eventAt, &expiresAt, &e.CreatedAt, &e.UpdatedAt,
		&lastUsed, &e.UseCount, &attrs,
	)
	if err != nil {
		return Entry{}, err
	}
	e.Simhash = simhash
	if eventAt.Valid {
		v := eventAt.Int64
		e.EventAt = &v
	}
	if expiresAt.Valid {
		v := expiresAt.Int64
		e.ExpiresAt = &v
	}
	if lastUsed.Valid {
		v := lastUsed.Int64
		e.LastUsedAt = &v
	}
	e.Attributes = map[string]any{}
	_ = json.Unmarshal([]byte(attrs), &e.Attributes)
	return e, nil
}

// Add inserts or merges a memory after exact/near duplicate and conflict checks.
func (s *Store) Add(ctx context.Context, in AddInput) (*AddResult, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("memstore: empty text")
	}
	in.Realm = normalizeRealm(in.Realm)
	in.Layer = normalizeLayer(in.Layer)
	if in.Scope == "" {
		in.Scope = "user"
	}
	if in.Audience == "" {
		in.Audience = "all"
	}
	if in.Confidence <= 0 {
		in.Confidence = 0.7
	}
	now := time.Now().UnixMilli()
	norm := Normalize(in.Text)
	hash := ContentHash(norm)
	sim := SimHash(norm)

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// 1. exact duplicate, including pending/archived rows so repeated imports
	// cannot create copies of the same memory.
	var existingID, existingStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT id, status FROM entries WHERE content_hash=?
		 ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'pending_review' THEN 1 ELSE 2 END
		 LIMIT 1`, hash).Scan(&existingID, &existingStatus)
	if err == nil {
		if err := mergeInto(ctx, tx, existingID, in, now); err != nil {
			return nil, err
		}
		if err := s.bumpRevision(ctx, tx); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		e, _ := s.Get(ctx, existingID)
		return &AddResult{Action: "duplicate", Entry: e, ExistingID: existingID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// 2. template fold
	if in.TemplateKey != "" {
		err = tx.QueryRowContext(ctx,
			`SELECT id FROM entries WHERE template_key=? AND status IN ('active','pending_review') LIMIT 1`,
			in.TemplateKey).Scan(&existingID)
		if err == nil {
			if err := mergeInto(ctx, tx, existingID, in, now); err != nil {
				return nil, err
			}
			if err := s.bumpRevision(ctx, tx); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			e, _ := s.Get(ctx, existingID)
			return &AddResult{Action: "merged", Entry: e, ExistingID: existingID}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	// 3. near duplicate within the same realm/layer
	rows, err := tx.QueryContext(ctx,
		`SELECT id, normalized, simhash FROM entries
		 WHERE status IN ('active','pending_review') AND realm=? AND layer=?
		 ORDER BY updated_at DESC LIMIT 300`,
		in.Realm, in.Layer)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, candidateNorm string
		var candidateSim int64
		if err := rows.Scan(&id, &candidateNorm, &candidateSim); err != nil {
			_ = rows.Close()
			return nil, err
		}
		// Chinese bigram Jaccard: 0.75 merges paraphrases such as
		// "避开花生" / "请避开" without collapsing unrelated short facts.
		if Jaccard(norm, candidateNorm) >= 0.75 || Hamming(sim, candidateSim) <= 3 {
			_ = rows.Close()
			if err := mergeInto(ctx, tx, id, in, now); err != nil {
				return nil, err
			}
			if err := s.bumpRevision(ctx, tx); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			e, _ := s.Get(ctx, id)
			return &AddResult{Action: "merged", Entry: e, ExistingID: id}, nil
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	// 4. conflict: same subject+predicate, different value
	var supersededID string
	if strings.TrimSpace(in.Subject) != "" && strings.TrimSpace(in.Predicate) != "" {
		var oldID, oldValue string
		err = tx.QueryRowContext(ctx,
			`SELECT id, value FROM entries
			 WHERE status='active' AND realm=? AND subject=? AND predicate=?
			 ORDER BY updated_at DESC LIMIT 1`,
			in.Realm, in.Subject, in.Predicate).Scan(&oldID, &oldValue)
		if err == nil && Normalize(oldValue) != Normalize(in.Value) {
			supersededID = oldID
			if _, err := tx.ExecContext(ctx,
				`UPDATE entries SET status='superseded', updated_at=? WHERE id=?`, now, oldID); err != nil {
				return nil, err
			}
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	status := strings.TrimSpace(in.ForceStatus)
	if status == "" {
		if in.Realm == RealmUnknown {
			status = StatusPending
		} else {
			status = StatusActive
		}
	}
	e := Entry{
		ID:              newID(),
		Realm:           in.Realm,
		Layer:           in.Layer,
		Scope:           in.Scope,
		Audience:        in.Audience,
		Subject:         strings.TrimSpace(in.Subject),
		Predicate:       strings.TrimSpace(in.Predicate),
		Value:           strings.TrimSpace(in.Value),
		Text:            strings.TrimSpace(in.Text),
		Normalized:      norm,
		ContentHash:     hash,
		Simhash:         sim,
		TemplateKey:     in.TemplateKey,
		Occurrences:     1,
		Importance:      clamp01(in.Importance),
		EmotionalWeight: clamp01(in.EmotionalWeight),
		Valence:         clamp(in.Valence, -1, 1),
		Confidence:      clamp01(in.Confidence),
		Status:          status,
		EventAt:         in.EventAt,
		ExpiresAt:       in.ExpiresAt,
		CreatedAt:       now,
		UpdatedAt:       now,
		Attributes:      in.Attributes,
	}
	if e.Importance == 0 {
		e.Importance = 0.5
	}
	attrs, _ := json.Marshal(e.Attributes)
	if e.Attributes == nil {
		attrs = []byte("{}")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO entries (`+entryColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Realm, e.Layer, e.Scope, e.Audience, e.Subject, e.Predicate, e.Value,
		e.Text, e.Normalized, e.ContentHash, e.Simhash, e.TemplateKey, e.ClusterID,
		e.Occurrences, e.Importance, e.EmotionalWeight, e.Valence, e.Confidence,
		e.Status, e.EventAt, e.ExpiresAt, e.CreatedAt, e.UpdatedAt, e.LastUsedAt,
		e.UseCount, string(attrs)); err != nil {
		return nil, err
	}
	if supersededID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO supersedes(entry_id,superseded_id,created_at) VALUES(?,?,?)`,
			e.ID, supersededID, now); err != nil {
			return nil, err
		}
	}
	if err := insertSource(ctx, tx, e.ID, in, now); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	action := "added"
	if supersededID != "" {
		action = "superseded"
	}
	return &AddResult{Action: action, Entry: &e, ExistingID: supersededID}, nil
}

func mergeInto(ctx context.Context, tx *sql.Tx, id string, in AddInput, now int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE entries SET occurrences=occurrences+1, updated_at=?,
		 importance=MAX(importance,?), confidence=MAX(confidence,?)
		 WHERE id=?`, now, clamp01(in.Importance), clamp01(in.Confidence), id); err != nil {
		return err
	}
	return insertSource(ctx, tx, id, in, now)
}

func insertSource(ctx context.Context, tx *sql.Tx, entryID string, in AddInput, now int64) error {
	sourceType := in.SourceType
	if sourceType == "" {
		sourceType = "manual"
	}
	_, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO sources(entry_id,source_type,source_id,session_key,created_at)
		 VALUES(?,?,?,?,?)`, entryID, sourceType, in.SourceID, in.SessionKey, now)
	return err
}

// Get loads one entry with its sources.
func (s *Store) Get(ctx context.Context, id string) (*Entry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE id=?`, id)
	e, err := scanEntry(row)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id,source_type,source_id,session_key,created_at FROM sources WHERE entry_id=? ORDER BY created_at`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var src Source
			if err := rows.Scan(&src.EntryID, &src.SourceType, &src.SourceID, &src.SessionKey, &src.CreatedAt); err == nil {
				e.Sources = append(e.Sources, src)
			}
		}
	}
	return &e, nil
}

// List returns filtered entries ordered by importance and recency.
func (s *Store) List(ctx context.Context, opts SearchOptions) ([]Entry, error) {
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
	if opts.Status != "" {
		where = append(where, "status=?")
		args = append(args, opts.Status)
	}
	if opts.Scope != "" {
		where = append(where, "scope=?")
		args = append(args, opts.Scope)
	}
	if opts.Audience != "" {
		where = append(where, "audience=?")
		args = append(args, opts.Audience)
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		where = append(where, "(text LIKE ? OR value LIKE ? OR subject LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE `+strings.Join(where, " AND ")+
			` ORDER BY importance DESC, updated_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Update applies editable fields to an entry.
func (s *Store) Update(ctx context.Context, id string, in AddInput) (*Entry, error) {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	sets := []string{"updated_at=?"}
	args := []any{now}
	if strings.TrimSpace(in.Text) != "" {
		norm := Normalize(in.Text)
		sets = append(sets, "text=?", "normalized=?", "content_hash=?", "simhash=?")
		args = append(args, strings.TrimSpace(in.Text), norm, ContentHash(norm), SimHash(norm))
	}
	if in.Realm != "" {
		sets = append(sets, "realm=?")
		args = append(args, normalizeRealm(in.Realm))
	}
	if in.Layer != "" {
		sets = append(sets, "layer=?")
		args = append(args, normalizeLayer(in.Layer))
	}
	if in.Audience != "" {
		sets = append(sets, "audience=?")
		args = append(args, in.Audience)
	}
	if in.Scope != "" {
		sets = append(sets, "scope=?")
		args = append(args, in.Scope)
	}
	if in.Subject != "" {
		sets = append(sets, "subject=?")
		args = append(args, strings.TrimSpace(in.Subject))
	}
	if in.Predicate != "" {
		sets = append(sets, "predicate=?")
		args = append(args, strings.TrimSpace(in.Predicate))
	}
	if in.Value != "" {
		sets = append(sets, "value=?")
		args = append(args, strings.TrimSpace(in.Value))
	}
	if in.Importance > 0 {
		sets = append(sets, "importance=?")
		args = append(args, clamp01(in.Importance))
	}
	if in.EmotionalWeight > 0 {
		sets = append(sets, "emotional_weight=?")
		args = append(args, clamp01(in.EmotionalWeight))
	}
	if in.ExpiresAt != nil {
		sets = append(sets, "expires_at=?")
		args = append(args, *in.ExpiresAt)
	}
	if in.ForceStatus != "" {
		sets = append(sets, "status=?")
		args = append(args, in.ForceStatus)
	}
	args = append(args, id)
	if _, err := tx.ExecContext(ctx, `UPDATE entries SET `+strings.Join(sets, ",")+` WHERE id=?`, args...); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// SetStatus changes an entry's lifecycle status.
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE entries SET status=?, updated_at=? WHERE id=?`, status, time.Now().UnixMilli(), id); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Overview returns dashboard counters.
func (s *Store) Overview(ctx context.Context) (Overview, error) {
	var o Overview
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM entries GROUP BY status`)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return o, err
		}
		o.Total += n
		switch status {
		case StatusActive:
			o.Active = n
		case StatusPending:
			o.Pending = n
		case StatusSuperseded:
			o.Superseded = n
		case StatusArchived:
			o.Archived = n
		}
	}
	o.Revision = s.Revision()
	if st, err := os.Stat(s.path); err == nil {
		o.DBSize = st.Size()
	}
	return o, rows.Err()
}

// Review returns entries needing human attention.
func (s *Store) Review(ctx context.Context, kind string) ([]Entry, error) {
	if kind == "conflict" {
		rows, err := s.db.QueryContext(ctx,
			`SELECT `+entryColumns+` FROM entries
			 WHERE status='active' AND subject<>'' AND predicate<>''
			   AND (realm, subject, predicate) IN (
			     SELECT realm, subject, predicate FROM entries
			     WHERE status='active' AND subject<>'' AND predicate<>''
			     GROUP BY realm, subject, predicate HAVING COUNT(*) > 1)
			 ORDER BY realm, subject, predicate, updated_at DESC LIMIT 200`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Entry
		for rows.Next() {
			e, err := scanEntry(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, rows.Err()
	}
	return s.List(ctx, SearchOptions{Status: StatusPending, Limit: 200})
}

// ResolveReview applies a review decision.
func (s *Store) ResolveReview(ctx context.Context, id, action, targetID string) error {
	switch action {
	case "approve":
		return s.SetStatus(ctx, id, StatusActive)
	case "archive":
		return s.SetStatus(ctx, id, StatusArchived)
	case "approve_fictional":
		_, err := s.Update(ctx, id, AddInput{Realm: RealmFictional, ForceStatus: StatusActive})
		return err
	case "approve_work":
		_, err := s.Update(ctx, id, AddInput{Realm: RealmWork, ForceStatus: StatusActive})
		return err
	case "merge":
		if targetID == "" {
			return fmt.Errorf("memstore: merge needs target_id")
		}
		return s.SetStatus(ctx, id, StatusSuperseded)
	default:
		return fmt.Errorf("memstore: unknown action %q", action)
	}
}

// Compact archives expired entries and old low-value episodic memories.
func (s *Store) Compact(ctx context.Context, retentionDays int) (int, error) {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE entries SET status='archived', updated_at=? WHERE status='active' AND expires_at IS NOT NULL AND expires_at < ?`,
		now, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -retentionDays).UnixMilli()
		res2, err := tx.ExecContext(ctx,
			`UPDATE entries SET status='archived', updated_at=?
			 WHERE status='active' AND layer='episodic' AND importance < 0.5 AND updated_at < ?`,
			now, cutoff)
		if err != nil {
			return int(n), err
		}
		n2, _ := res2.RowsAffected()
		n += n2
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return int(n), err
	}
	return int(n), tx.Commit()
}

func normalizeRealm(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case RealmFictional:
		return RealmFictional
	case RealmWork:
		return RealmWork
	case RealmUnknown:
		return RealmUnknown
	default:
		return RealmReal
	}
}

func normalizeLayer(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case LayerPersona, LayerProfile, LayerRelationship, LayerEpisodic, LayerProcedural, LayerBoundary:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return LayerProfile
	}
}

func clamp01(v float64) float64 { return clamp(v, 0, 1) }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// newID returns a sortable unique ID.
func newID() string {
	return fmt.Sprintf("mem_%d_%s", time.Now().UnixNano(), randomHex(6))
}
