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
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("memstore: empty ledger path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("memstore: ledger dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memstore: ledger open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=FULL", // the ledger must survive a host crash
		"PRAGMA busy_timeout=5000",
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
	if err := l.loadOrCreateIdentity(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return l, nil
}

func (l *ForgetLedger) loadOrCreateIdentity() error {
	uuid, err := l.meta("ledger_uuid")
	if err != nil {
		return err
	}
	keyB64, err := l.meta("hmac_key")
	if err != nil {
		return err
	}
	keyID, err := l.meta("key_id")
	if err != nil {
		return err
	}
	if uuid != "" && keyB64 != "" && keyID != "" {
		key, decErr := base64.StdEncoding.DecodeString(keyB64)
		if decErr == nil && len(key) == 32 {
			l.uuid, l.keyID, l.key = uuid, keyID, key
			return nil
		}
	}
	// First use, or an unreadable key: issue a fresh identity and record it.
	// A corrupted ledger never silently reuses a partial key.
	newUUID := randomUUID()
	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		return fmt.Errorf("memstore: ledger key: %w", err)
	}
	newKeyID := "k_" + newUUID[:8]
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, v := range map[string]string{
		"ledger_uuid": newUUID,
		"hmac_key":    base64.StdEncoding.EncodeToString(newKey),
		"key_id":      newKeyID,
		"created_at":  fmt.Sprintf("%d", time.Now().UnixMilli()),
	} {
		if _, err := tx.Exec(
			`INSERT INTO ledger_meta(key,value) VALUES(?,?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v); err != nil {
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
	res, err := l.db.ExecContext(ctx,
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
		out["reason"] = "ledger not opened"
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
