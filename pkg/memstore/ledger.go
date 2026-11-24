package memstore

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ForgetLedger is the independent, append-only journal for accepted forget
// obligations. It deliberately lives outside memory.db: restoring an older
// database (or losing its outbox) must not resurrect forgotten content.
//
// The ledger never stores memory text. Selectors are versioned HMAC digests;
// target manifests only carry identifiers and locating hints.
type ForgetLedger struct {
	db    *sql.DB
	path  string
	uuid  string
	keyID string
	key   []byte
	mu    sync.Mutex
}

// ForgetIntent is one journal row.
type ForgetIntent struct {
	Seq            int64  `json:"seq"`
	LedgerUUID     string `json:"ledger_uuid"`
	RequestID      string `json:"request_id"`
	Namespace      string `json:"namespace,omitempty"`
	DomainID       string `json:"domain_id,omitempty"`
	ScopeJSON      string `json:"scope_json,omitempty"`
	SelectorType   string `json:"selector_type"`
	SelectorDigest string `json:"selector_digest"`
	HashVersion    string `json:"hash_version"`
	KeyID          string `json:"key_id,omitempty"`
	TargetManifest string `json:"target_manifest,omitempty"`
	Status         string `json:"status"`
	AppliedSeq     int64  `json:"applied_seq,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}

// ForgetTargetManifest is the minimal set of identifiers needed to replay a
// cleanup without the deleted rows: entry ids, relationship-state keys,
// content digests (never text) and message locators.
type ForgetTargetManifest struct {
	EntryIDs         []string        `json:"entry_ids,omitempty"`
	RelationshipKeys []string        `json:"relationship_keys,omitempty"`
	CapsuleIDs       []string        `json:"capsule_ids,omitempty"`
	OriginEventIDs   []string        `json:"origin_event_ids,omitempty"`
	Locators         []SourceLocator `json:"locators,omitempty"`
}

// LedgerPathFor derives the ledger path next to the database file.
func LedgerPathFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "forget-ledger.db")
}

// OpenForgetLedger opens or creates the independent ledger. The HMAC key is
// generated once, stored in the ledger, and never leaves the host.
func OpenForgetLedger(path string) (*ForgetLedger, error) {
	return openForgetLedger(path, true)
}

// OpenExistingForgetLedger opens a ledger that must already exist and carry a
// complete identity. A missing or damaged ledger is an error: silently creating
// a new empty identity would invalidate every historical HMAC selector.
func OpenExistingForgetLedger(path string) (*ForgetLedger, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("memstore: empty ledger path")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("memstore: forget ledger missing: %w", err)
	}
	return openForgetLedger(path, false)
}

func openForgetLedger(path string, allowCreate bool) (*ForgetLedger, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("memstore: empty ledger path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("memstore: ledger dir: %w", err)
	}
	dsn := path
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("memstore: ledger open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL", // the ledger must survive a host crash
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memstore: ledger %s: %w", pragma, err)
		}
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS ledger_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS forget_journal (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			ledger_uuid TEXT NOT NULL,
			request_id TEXT NOT NULL,
			namespace TEXT NOT NULL DEFAULT '',
			domain_id TEXT NOT NULL DEFAULT '',
			scope_json TEXT NOT NULL DEFAULT '{}',
			selector_type TEXT NOT NULL,
			selector_digest TEXT NOT NULL,
			hash_version TEXT NOT NULL DEFAULT 'hmac-sha256-v1',
			key_id TEXT NOT NULL DEFAULT '',
			target_manifest TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending',
			applied_seq INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS ix_forget_journal_status ON forget_journal(status, seq)`,
		`CREATE INDEX IF NOT EXISTS ix_forget_journal_digest ON forget_journal(selector_digest)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("memstore: ledger schema: %w", err)
		}
	}
	_ = os.Chmod(path, 0o600)

	l := &ForgetLedger{db: db, path: path}
	if err := l.loadOrCreateIdentity(allowCreate); err != nil {
		_ = db.Close()
		return nil, err
	}
	return l, nil
}

func (l *ForgetLedger) loadOrCreateIdentity(allowCreate bool) error {
	tx, err := l.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var uuid, keyB64, keyID string
	if err := tx.QueryRow(
		`SELECT
		   COALESCE(MAX(CASE WHEN key='ledger_uuid' THEN value END),''),
		   COALESCE(MAX(CASE WHEN key='hmac_key' THEN value END),''),
		   COALESCE(MAX(CASE WHEN key='key_id' THEN value END),'')
		 FROM ledger_meta WHERE key IN ('ledger_uuid','hmac_key','key_id')`).
		Scan(&uuid, &keyB64, &keyID); err != nil {
		return err
	}
	if uuid != "" && keyB64 != "" && keyID != "" {
		key, decErr := base64.StdEncoding.DecodeString(keyB64)
		if decErr != nil || len(key) != 32 {
			return fmt.Errorf("memstore: forget ledger identity is corrupt (unreadable HMAC key); restore the ledger backup")
		}
		l.uuid, l.keyID, l.key = uuid, keyID, key
		return tx.Rollback()
	}
	if uuid != "" || keyB64 != "" || keyID != "" {
		return fmt.Errorf("memstore: forget ledger identity is incomplete; restore the ledger backup")
	}
	var journalRows int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM forget_journal`).Scan(&journalRows); err != nil {
		return err
	}
	if !allowCreate || journalRows > 0 {
		return fmt.Errorf(
			"memstore: forget ledger has no usable identity and %d journal row(s); refusing to create a new identity",
			journalRows)
	}

	// First use of a brand-new ledger: issue the identity exactly once. The
	// ledger DSN uses _txlock=immediate, so concurrent first opens serialize
	// here and the second one observes the committed identity above.
	newUUID := randomUUID()
	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		return fmt.Errorf("memstore: ledger key: %w", err)
	}
	newKeyID := "k_" + newUUID[:8]
	for k, v := range map[string]string{
		"ledger_uuid": newUUID,
		"hmac_key":    base64.StdEncoding.EncodeToString(newKey),
		"key_id":      newKeyID,
		"created_at":  fmt.Sprintf("%d", time.Now().UnixMilli()),
	} {
		if _, err := tx.Exec(
			`INSERT INTO ledger_meta(key,value) VALUES(?,?)`, k, v); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	l.uuid, l.keyID, l.key = newUUID, newKeyID, newKey
	return nil
}

func (l *ForgetLedger) meta(key string) (string, error) {
	var v string
	err := l.db.QueryRow(`SELECT value FROM ledger_meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// Close flushes WAL and closes the ledger.
func (l *ForgetLedger) Close() error {
	if l == nil || l.db == nil {
		return nil
	}
	_, _ = l.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return l.db.Close()
}

// Path returns the ledger file path.
func (l *ForgetLedger) Path() string { return l.path }

// UUID returns the stable ledger identity.
func (l *ForgetLedger) UUID() string { return l.uuid }

// KeyID returns the active HMAC key id, recorded in each tombstone.
func (l *ForgetLedger) KeyID() string { return l.keyID }

// Digest returns the versioned HMAC of normalized text. The digest is what
// Add uses to block re-learning; the text itself is never stored.
func (l *ForgetLedger) Digest(text string) (digest, keyID, hashVersion string) {
	mac := hmac.New(sha256.New, l.key)
	mac.Write([]byte(Normalize(text)))
	return hex.EncodeToString(mac.Sum(nil)), l.keyID, "hmac-sha256-v1"
}

// RecordIntent persists a forget obligation before the main database is
// touched. A failure here means the obligation was never accepted.
func (l *ForgetLedger) RecordIntent(ctx context.Context, in ForgetIntent) (ForgetIntent, error) {
	if strings.TrimSpace(in.RequestID) == "" {
		in.RequestID = randomUUID()
	}
	if strings.TrimSpace(in.SelectorType) == "" || strings.TrimSpace(in.SelectorDigest) == "" {
		return ForgetIntent{}, fmt.Errorf("memstore: ledger intent needs selector type and digest")
	}
	if in.HashVersion == "" {
		in.HashVersion = "hmac-sha256-v1"
	}
	if in.KeyID == "" {
		in.KeyID = l.keyID
	}
	if in.ScopeJSON == "" {
		in.ScopeJSON = "{}"
	}
	if in.TargetManifest == "" {
		in.TargetManifest = "{}"
	}
	in.LedgerUUID = l.uuid
	in.Status = "pending"
	in.CreatedAt = time.Now().UnixMilli()

	l.mu.Lock()
	defer l.mu.Unlock()
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return ForgetIntent{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing ForgetIntent
	err = tx.QueryRowContext(ctx,
		`SELECT seq,ledger_uuid,request_id,namespace,domain_id,scope_json,selector_type,
		        selector_digest,hash_version,key_id,target_manifest,status,applied_seq,created_at
		 FROM forget_journal WHERE request_id=? ORDER BY seq LIMIT 1`, in.RequestID).
		Scan(&existing.Seq, &existing.LedgerUUID, &existing.RequestID, &existing.Namespace,
			&existing.DomainID, &existing.ScopeJSON, &existing.SelectorType,
			&existing.SelectorDigest, &existing.HashVersion, &existing.KeyID,
			&existing.TargetManifest, &existing.Status, &existing.AppliedSeq, &existing.CreatedAt)
	if err == nil {
		if existing.DomainID != in.DomainID || existing.SelectorDigest != in.SelectorDigest ||
			existing.TargetManifest != in.TargetManifest {
			return ForgetIntent{}, fmt.Errorf("memstore: request_id %q already names a different forget intent", in.RequestID)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ForgetIntent{}, err
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO forget_journal
		 (ledger_uuid,request_id,namespace,domain_id,scope_json,selector_type,selector_digest,
		  hash_version,key_id,target_manifest,status,applied_seq,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,'pending',0,?)`,
		in.LedgerUUID, in.RequestID, in.Namespace, in.DomainID, in.ScopeJSON,
		in.SelectorType, in.SelectorDigest, in.HashVersion, in.KeyID,
		in.TargetManifest, in.CreatedAt)
	if err != nil {
		return ForgetIntent{}, err
	}
	in.Seq, _ = res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return ForgetIntent{}, err
	}
	return in, nil
}

// MarkApplied records that the main database finished applying this intent.
func (l *ForgetLedger) MarkApplied(ctx context.Context, seq, appliedSeq int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.ExecContext(ctx,
		`UPDATE forget_journal SET status='applied', applied_seq=? WHERE seq=? AND status<>'blocked'`,
		appliedSeq, seq)
	return err
}

// MarkBlocked keeps an obligation alive when cleanup cannot finish.
func (l *ForgetLedger) MarkBlocked(ctx context.Context, seq int64, reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.ExecContext(ctx,
		`UPDATE forget_journal SET status='blocked' WHERE seq=?`, seq)
	return err
}

// Pending returns obligations that were accepted but not applied.
func (l *ForgetLedger) Pending(ctx context.Context) ([]ForgetIntent, error) {
	return l.queryIntents(ctx,
		`SELECT seq,ledger_uuid,request_id,namespace,domain_id,scope_json,selector_type,
		        selector_digest,hash_version,key_id,target_manifest,status,applied_seq,created_at
		 FROM forget_journal WHERE status='pending' ORDER BY seq`)
}

// Recent returns the newest intents for the dashboard.
func (l *ForgetLedger) Recent(ctx context.Context, limit int) ([]ForgetIntent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return l.queryIntents(ctx,
		`SELECT seq,ledger_uuid,request_id,namespace,domain_id,scope_json,selector_type,
		        selector_digest,hash_version,key_id,target_manifest,status,applied_seq,created_at
		 FROM forget_journal ORDER BY seq DESC LIMIT ?`, limit)
}

func (l *ForgetLedger) queryIntents(ctx context.Context, q string, args ...any) ([]ForgetIntent, error) {
	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ForgetIntent{}
	for rows.Next() {
		var in ForgetIntent
		if err := rows.Scan(&in.Seq, &in.LedgerUUID, &in.RequestID, &in.Namespace, &in.DomainID,
			&in.ScopeJSON, &in.SelectorType, &in.SelectorDigest, &in.HashVersion, &in.KeyID,
			&in.TargetManifest, &in.Status, &in.AppliedSeq, &in.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// HasDigest reports whether a digest was ever forgotten. It is the cross-
// backup guard used by Add before learning the same sentence again.
func (l *ForgetLedger) HasDigest(ctx context.Context, digest string) (bool, error) {
	if l == nil || digest == "" {
		return false, nil
	}
	var one int
	err := l.db.QueryRowContext(ctx,
		`SELECT 1 FROM forget_journal WHERE selector_digest=? LIMIT 1`, digest).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AllDigests returns every forgotten digest for replay against a restored DB.
func (l *ForgetLedger) AllDigests(ctx context.Context) ([]string, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT DISTINCT selector_digest FROM forget_journal WHERE selector_digest<>'' ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (l *ForgetLedger) watermark(ctx context.Context) (maxSeq, maxAppliedSeq, pending int64, err error) {
	err = l.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0),
		        COALESCE(MAX(CASE WHEN status='applied' THEN applied_seq END),0),
		        COALESCE(SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),0)
		 FROM forget_journal`).Scan(&maxSeq, &maxAppliedSeq, &pending)
	return maxSeq, maxAppliedSeq, pending, err
}

// VerifyLedgerBackup compares a ledger backup with the active ledger identity.
// Restoring a database with a ledger from another identity would make every
// historical HMAC selector unusable, so this check is explicit.
func (s *Store) VerifyLedgerBackup(backupPath string) (map[string]any, error) {
	if s == nil || s.ledger == nil {
		return nil, fmt.Errorf("memstore: active forget ledger unavailable")
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, fmt.Errorf("memstore: read ledger backup: %w", err)
	}
	// Verify a copy so a read-only backup directory and sidecar files are not
	// touched by opening the ledger in WAL mode.
	tmpDir, err := os.MkdirTemp("", "picoclaw-ledger-verify-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, "forget-ledger.db")
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return nil, err
	}
	backup, err := OpenExistingForgetLedger(tmpPath)
	if err != nil {
		return nil, err
	}
	defer backup.Close()
	match := backup.UUID() == s.ledger.UUID() && backup.KeyID() == s.ledger.KeyID()
	backupSeq, backupApplied, backupPending, err := backup.watermark(context.Background())
	if err != nil {
		return nil, err
	}
	activeSeq, activeApplied, activePending, err := s.ledger.watermark(context.Background())
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"backup_uuid":        backup.UUID(),
		"backup_key_id":      backup.KeyID(),
		"backup_max_seq":     backupSeq,
		"backup_applied_seq": backupApplied,
		"backup_pending":     backupPending,
		"active_uuid":        s.ledger.UUID(),
		"active_key_id":      s.ledger.KeyID(),
		"active_max_seq":     activeSeq,
		"active_applied_seq": activeApplied,
		"active_pending":     activePending,
		"identity_match":     match,
		"match":              match,
		"watermark_equal":    backupSeq == activeSeq && backupPending == activePending,
		"backup_is_older":    backupSeq < activeSeq,
		"backup_is_newer":    backupSeq > activeSeq,
	}
	if !match {
		return out, fmt.Errorf("memstore: ledger backup identity does not match the active ledger")
	}
	return out, nil
}

// BackupTo writes a consistent snapshot of the ledger (including committed WAL
// pages) without copying a live SQLite file. The destination must not exist.
func (l *ForgetLedger) BackupTo(ctx context.Context, dst string) error {
	if l == nil || l.db == nil {
		return fmt.Errorf("memstore: forget ledger unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("memstore: backup ledger: %w", err)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return fmt.Errorf("memstore: chmod ledger backup: %w", err)
	}
	return nil
}

func randomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ensureLedger opens the ledger for a store, once. A store without a healthy
// ledger runs in degraded mode: reads still work, destructive writes fail
// closed because the obligation could not be journaled.
func (s *Store) ensureLedger() error {
	if s.ledger != nil {
		return nil
	}
	path := s.ledgerPath
	if path == "" {
		path = LedgerPathFor(s.path)
	}
	if _, err := os.Stat(path); err == nil {
		l, err := OpenExistingForgetLedger(path)
		if err != nil {
			return err
		}
		s.ledger = l
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("memstore: inspect forget ledger: %w", err)
	}
	// Only a brand-new database or a v3 database being upgraded may create its
	// first ledger. An existing v4 database that lost its ledger must restore
	// the backup instead of silently issuing a new identity.
	if !s.fresh && s.openedSchema >= CurrentSchemaVersion {
		return fmt.Errorf(
			"memstore: forget ledger missing at %s; restore the ledger backup before destructive forget", path)
	}
	l, err := OpenForgetLedger(path)
	if err != nil {
		return err
	}
	s.ledger = l
	return nil
}

// ForgetLedgerRef returns the opened ledger, or nil when it is unavailable.
func (s *Store) ForgetLedgerRef() *ForgetLedger { return s.ledger }

// LedgerHealth reports ledger availability for the dashboard. A missing
// ledger is a degraded state, never "healthy".
func (s *Store) LedgerHealth(ctx context.Context) (map[string]any, error) {
	out := map[string]any{"available": false}
	if s.ledger == nil {
		reason := "ledger not opened"
		if s.ledgerErr != nil {
			reason = s.ledgerErr.Error()
		}
		out["reason"] = reason
		return out, nil
	}
	out["available"] = true
	out["path"] = s.ledger.Path()
	out["ledger_uuid"] = s.ledger.UUID()
	out["key_id"] = s.ledger.KeyID()
	pending, err := s.ledger.Pending(ctx)
	if err != nil {
		out["available"] = false
		out["reason"] = err.Error()
		return out, nil
	}
	out["pending"] = len(pending)
	return out, nil
}

// MarshalManifest is a helper that keeps manifest JSON deterministic.
func MarshalManifest(m ForgetTargetManifest) string {
	data, _ := json.Marshal(m)
	return string(data)
}

// ParseManifest decodes a stored manifest.
func ParseManifest(raw string) (ForgetTargetManifest, error) {
	var m ForgetTargetManifest
	if strings.TrimSpace(raw) == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return m, err
	}
	return m, nil
}
