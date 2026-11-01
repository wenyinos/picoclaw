package memstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CurrentSchemaVersion is the schema version expected by this build.
const CurrentSchemaVersion = 3

// MigrationStatus describes whether the database needs a schema or data
// migration. It is safe to call while the service is running.
type MigrationStatus struct {
	SchemaVersion          int    `json:"schema_version"`
	CurrentVersion         int    `json:"current_version"`
	NeedsMigration         bool   `json:"needs_migration"`
	NeedsSchemaMigration   bool   `json:"needs_schema_migration"`
	NeedsDataMigration     bool   `json:"needs_data_migration"`
	EmptyPredicate         int    `json:"empty_predicate"`
	EmptyRelationshipFacet int    `json:"empty_relationship_facet"`
	Relationship           int    `json:"relationship"`
	Personas               int    `json:"personas"`
	PinnedCore             int    `json:"pinned_core"`
	Relations              int    `json:"relations"`
	FTSRows                int    `json:"fts_rows"`
	FTSTokenizer           string `json:"fts_tokenizer,omitempty"`
	Total                  int    `json:"total"`
	Active                 int    `json:"active"`
	DBPath                 string `json:"db_path"`
	DBSize                 int64  `json:"db_size"`
	Backup                 string `json:"backup,omitempty"`
	LastMigrationAt        int64  `json:"last_migration_at,omitempty"`
}

// MigrationReport is returned after applying the migration chain.
type MigrationReport struct {
	PreviousSchemaVersion   int    `json:"previous_schema_version"`
	SchemaVersion           int    `json:"schema_version"`
	Total                   int    `json:"total"`
	Active                  int    `json:"active"`
	EmptyPredicate          int    `json:"empty_predicate"`
	UpdatedPredicates       int    `json:"updated_predicates"`
	MappedRelationships     int    `json:"mapped_relationships"`
	MappedPredicates        int    `json:"mapped_predicates"`
	Reclassified            int    `json:"reclassified"`
	Restored                int    `json:"restored"`
	BackfilledSources       int    `json:"backfilled_sources"`
	DroppedOrphanSources    int    `json:"dropped_orphan_sources"`
	DroppedOrphanSupersedes int    `json:"dropped_orphan_supersedes"`
	FTSRows                 int    `json:"fts_rows"`
	FTSTokenizer            string `json:"fts_tokenizer"`
	Backup                  string `json:"backup"`
}

type relationshipFacetMapping struct {
	predicate string
	facet     string
}

// relationshipFacetMappings covers the v13-v21 relationship entries that
// existed when schema v2 was introduced. They share an empty predicate but
// are distinct facets, so they must not be collapsed into one bucket.
var relationshipFacetMappings = map[string]relationshipFacetMapping{
	"mem_1793291583022394804_389b234ff07a": {predicate: "身份关系", facet: "公开身份"},
	"mem_1793291885976466654_95b96ef1a4f9": {predicate: "身份关系", facet: "家庭边界"},
	"mem_1793292294971378150_0af7465383c9": {predicate: "身份关系", facet: "家庭压力"},
	"mem_1793292513257056884_2e137cb26ec6": {predicate: "身份关系", facet: "家庭压力"},
	"mem_1793292854557996374_10c4f737c415": {predicate: "身份关系", facet: "未来规划"},
	"mem_1793293169053496943_d8e84bbbedc0": {predicate: "权力结构", facet: "欲望宣示"},
	"mem_1793293677975808379_484086f37aae": {predicate: "心理状态", facet: "道德账本"},
	"mem_1793294105315856968_5f3e83628cb5": {predicate: "事件·第一次", facet: "真相修正"},
	"mem_1793294608257053406_2c4aeb68e2b8": {predicate: "事件·第一次", facet: "真相披露"},
}

// v3PredicateMappings reclassifies the reference database's remaining
// 待分类 entries. The IDs are stable because memstore IDs never change.
var v3PredicateMappings = map[string]relationshipFacetMapping{
	"mem_1793482597225392486_d8b4d02583d0": {predicate: "记忆加载机制", facet: "加载机制与锚词"},
	"mem_1793484881062066614_1feaad9e8781": {predicate: "外貌", facet: "造型体系补充"},
	"mem_1793485792475128563_c87b05aa8668": {predicate: "日常层", facet: "日常基调"},
	"mem_1793485807406295562_8df1a6cefbd4": {predicate: "场景", facet: "一天·撒娇版v3定稿"},
	"mem_1793486010139840130_9d15c41d1215": {predicate: "日常层", facet: "生活全景·分栏总集"},
	"mem_1793486024978862407_b3781c6af32c": {predicate: "日常层", facet: "社交·节日·旅行·公共场合"},
	"mem_1793486047021908668_c294bd5e3c2e": {predicate: "日常层", facet: "日常人格·自由度·升级路径"},
	"mem_1793486047024473426_a5d832fba732": {predicate: "写作规则", facet: "适用范围勘误"},
	"mem_1793486594591599593_f50c0d0089c1": {predicate: "日常层", facet: "记账机制"},
	"mem_1793486929344429223_009ed44cf8d0": {predicate: "权力结构", facet: "欲望处理流程"},
	"mem_1793521634131564327_85d7549aca02": {predicate: "场景", facet: "约定戏码·假装被侵犯"},
	"mem_1793522088347460077_62d9ca2a2a1c": {predicate: "场景", facet: "开局宣告台词"},
	"mem_1793522313671742095_d4c3c3a18040": {predicate: "场景", facet: "高潮段·说你爱我"},
	"mem_1793522476207926084_125c1e1e9517": {predicate: "道具", facet: "薄丝袜与撕裂声"},
}

const (
	// v3PollutionEntry is a line of dialogue that was stored as a real profile
	// fact. It is reclassified, not merged: the original wording is material.
	v3PollutionEntry = "mem_1793522100304491181_1e963afd8670"
	// v3BirthdayEntry was archived by mistake and has to be active again.
	v3BirthdayEntry = "mem_1792917685707659889_b898d4bfcfd6"
	// v3ManualSourceEntry is the one hand-written entry without a source.
	v3ManualSourceEntry = "mem_1793459045418_8ee6a0820d74"
)

func (s *Store) schemaVersionLocked(ctx context.Context) int {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='schema_version'`).Scan(&raw)
	if err != nil {
		return 0
	}
	var v int
	_, _ = fmt.Sscanf(raw, "%d", &v)
	return v
}

func (s *Store) metaValueLocked(ctx context.Context, key string) string {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&raw); err != nil {
		return ""
	}
	return raw
}

func (s *Store) setMetaLocked(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO meta(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) tableExistsLocked(ctx context.Context, table string) bool {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&one)
	return err == nil
}

func (s *Store) triggerExistsLocked(ctx context.Context, trigger string) bool {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM sqlite_master WHERE type='trigger' AND name=?`, trigger).Scan(&one)
	return err == nil
}

func (s *Store) hasColumnLocked(ctx context.Context, table, column string) bool {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

func (s *Store) personasHasCompositeKeyLocked(ctx context.Context) bool {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(personas)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	pk := map[string]int{}
	for rows.Next() {
		var cid, notNull, pkPos int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pkPos); err != nil {
			return false
		}
		pk[name] = pkPos
	}
	return pk["id"] > 0 && pk["version"] > 0
}

// backupWithRotation writes a VACUUM INTO snapshot under memory/backups and
// keeps only the newest two files.
func (s *Store) backupWithRotation(ctx context.Context, label string) (string, error) {
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, "memory.db.bak-"+time.Now().Format("20060102-150405"))
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = fmt.Sprintf("%s-%d", dst, i)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return "", fmt.Errorf("backup %s: %w", label, err)
	}
	_ = os.Chmod(dst, 0o600)
	s.rotateBackups(dir, 2)
	return dst, nil
}

func (s *Store) rotateBackups(dir string, keep int) {
	files, _ := filepath.Glob(filepath.Join(dir, "memory.db.bak-*"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	for i, f := range files {
		if i >= keep {
			_ = os.Remove(f)
		}
	}
}

// migrateSchema upgrades an existing v1 database through v2 to v3. It is
// idempotent and safe to call on every Open.
func (s *Store) migrateSchema(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.migrateSchemaLocked(ctx)
	return err
}

type v2Plan struct {
	hasFacet       bool
	hasExpires     bool
	hasHistory     bool
	personasExists bool
	personasV2     bool
}

type v3Plan struct {
	have map[string]bool
}

type ftsPlan struct {
	available bool
	tokenizer string
}

func (s *Store) planV2(ctx context.Context) v2Plan {
	return v2Plan{
		hasFacet:       s.hasColumnLocked(ctx, "entries", "facet"),
		hasExpires:     s.hasColumnLocked(ctx, "relationship_state", "expires_at"),
		hasHistory:     s.tableExistsLocked(ctx, "relationship_state_history"),
		personasExists: s.tableExistsLocked(ctx, "personas"),
		personasV2: s.hasColumnLocked(ctx, "personas", "realm") &&
			s.hasColumnLocked(ctx, "personas", "audience") &&
			s.hasColumnLocked(ctx, "personas", "scope") &&
			s.personasHasCompositeKeyLocked(ctx),
	}
}

func (p v2Plan) complete() bool {
	return p.hasFacet && p.hasExpires && p.hasHistory && p.personasV2
}

// planV3Columns must run before BeginTx: Store uses MaxOpenConns(1), so any
// s.db query while the transaction holds the connection would deadlock.
func (s *Store) planV3Columns(ctx context.Context) v3Plan {
	p := v3Plan{have: map[string]bool{}}
	for _, c := range []string{
		"event_time_precision", "event_timezone", "event_time_source",
		"valid_from", "valid_to", "pinned_core",
	} {
		p.have[c] = s.hasColumnLocked(ctx, "entries", c)
	}
	return p
}

// detectFTS probes FTS5 and the trigram tokenizer before the transaction. A
// build without trigram falls back to unicode61 instead of failing migration.
func (s *Store) detectFTS(ctx context.Context) ftsPlan {
	var enabled int
	if err := s.db.QueryRowContext(ctx,
		`SELECT sqlite_compileoption_used('ENABLE_FTS5')`).Scan(&enabled); err != nil || enabled != 1 {
		return ftsPlan{tokenizer: "none"}
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE VIRTUAL TABLE IF NOT EXISTS _trigram_probe USING fts5(content, tokenize="trigram")`); err == nil {
		_, _ = s.db.ExecContext(ctx, `DROP TABLE IF EXISTS _trigram_probe`)
		return ftsPlan{available: true, tokenizer: "trigram"}
	}
	return ftsPlan{available: true, tokenizer: "unicode61"}
}

func (s *Store) v3CompleteLocked(ctx context.Context, fts ftsPlan) bool {
	if !s.hasColumnLocked(ctx, "entries", "valid_to") ||
		!s.hasColumnLocked(ctx, "entries", "pinned_core") ||
		!s.hasColumnLocked(ctx, "entries", "event_time_precision") ||
		!s.hasColumnLocked(ctx, "sources", "confidence") ||
		!s.tableExistsLocked(ctx, "memory_relations") ||
		!s.tableExistsLocked(ctx, "memory_operations") ||
		!s.tableExistsLocked(ctx, "memory_clusters") ||
		!s.tableExistsLocked(ctx, "memory_cluster_members") ||
		!s.tableExistsLocked(ctx, "memory_summary_versions") {
		return false
	}
	if fts.available {
		if !s.tableExistsLocked(ctx, "entries_fts") ||
			!s.triggerExistsLocked(ctx, "entries_ai") ||
			!s.triggerExistsLocked(ctx, "entries_ad") ||
			!s.triggerExistsLocked(ctx, "entries_au") {
			return false
		}
	}
	return true
}

func (s *Store) migrateSchemaLocked(ctx context.Context) (int, string, error) {
	previous := s.schemaVersionLocked(ctx)
	planV2 := s.planV2(ctx)
	planV3 := s.planV3Columns(ctx)
	fts := s.detectFTS(ctx)

	needV2 := !planV2.complete()
	needV3 := !s.v3CompleteLocked(ctx, fts)
	if !needV2 && !needV3 && previous >= CurrentSchemaVersion {
		return previous, "", nil
	}

	backup := ""
	hasExistingData := false
	if s.tableExistsLocked(ctx, "entries") {
		var n int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&n) == nil && n > 0 {
			hasExistingData = true
		}
	}
	if previous > 0 || hasExistingData {
		var err error
		backup, err = s.backupWithRotation(ctx, "schema-v3")
		if err != nil {
			return previous, "", err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return previous, backup, err
	}
	defer func() { _ = tx.Rollback() }()

	if needV2 {
		if err := s.migrateToV2Locked(ctx, tx, planV2); err != nil {
			return previous, backup, err
		}
	}
	if err := s.migrateToV3Locked(ctx, tx, planV3, fts); err != nil {
		return previous, backup, err
	}

	// The version is written only after both segments succeeded (B1).
	if err := s.setMetaLocked(ctx, tx, "schema_version", fmt.Sprintf("%d", CurrentSchemaVersion)); err != nil {
		return previous, backup, err
	}
	if err := s.setMetaLocked(ctx, tx, "fts_tokenizer", fts.tokenizer); err != nil {
		return previous, backup, err
	}
	if backup != "" {
		if err := s.setMetaLocked(ctx, tx, "last_migration_backup", backup); err != nil {
			return previous, backup, err
		}
		if err := s.setMetaLocked(ctx, tx, "last_migration_at",
			fmt.Sprintf("%d", time.Now().UnixMilli())); err != nil {
			return previous, backup, err
		}
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return previous, backup, err
	}
	if err := tx.Commit(); err != nil {
		return previous, backup, err
	}
	return previous, backup, nil
}

// migrateToV2Locked is the original v1 → v2 upgrade. Every object is guarded
// by a precomputed flag, so the segment can run on an already-upgraded DB.
func (s *Store) migrateToV2Locked(ctx context.Context, tx *sql.Tx, plan v2Plan) error {
	if !plan.hasFacet {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE entries ADD COLUMN facet TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("v2 add facet: %w", err)
		}
	}
	if !plan.hasExpires {
		if _, err := tx.ExecContext(ctx,
			`ALTER TABLE relationship_state ADD COLUMN expires_at INTEGER`); err != nil {
			return fmt.Errorf("v2 add expires_at: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS relationship_state_history (
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		valid_from INTEGER NOT NULL,
		valid_to INTEGER NOT NULL,
		confidence REAL NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (key, valid_from)
	)`); err != nil {
		return fmt.Errorf("v2 history table: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS ix_entries_facet ON entries(realm, subject, predicate, facet, status)`); err != nil {
		return fmt.Errorf("v2 facet index: %w", err)
	}

	if !plan.personasV2 {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS personas_v2`); err != nil {
			return fmt.Errorf("v2 drop personas_v2: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE personas_v2 (
			id TEXT NOT NULL,
			name TEXT NOT NULL,
			version INTEGER NOT NULL,
			content TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 0,
			realm TEXT NOT NULL DEFAULT 'real',
			audience TEXT NOT NULL DEFAULT 'private',
			scope TEXT NOT NULL DEFAULT 'user',
			created_at INTEGER NOT NULL,
			PRIMARY KEY (id, version)
		)`); err != nil {
			return fmt.Errorf("v2 create personas_v2: %w", err)
		}
		if plan.personasExists {
			if _, err := tx.ExecContext(ctx, `INSERT INTO personas_v2
				(id,name,version,content,active,realm,audience,scope,created_at)
				SELECT id,name,version,content,active,'real','private','user',created_at FROM personas`); err != nil {
				return fmt.Errorf("v2 copy personas: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DROP TABLE personas`); err != nil {
				return fmt.Errorf("v2 drop personas: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE personas_v2 RENAME TO personas`); err != nil {
			return fmt.Errorf("v2 rename personas: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS ix_personas_active ON personas(active, realm, audience)`); err != nil {
		return fmt.Errorf("v2 persona index: %w", err)
	}
	return nil
}

func (s *Store) migrateToV3Locked(ctx context.Context, tx *sql.Tx, plan v3Plan, fts ftsPlan) error {
	// Segment A: non-idempotent ALTERs, guarded by the precomputed plan.
	for _, col := range []struct{ name, ddl string }{
		{"event_time_precision", `ALTER TABLE entries ADD COLUMN event_time_precision TEXT NOT NULL DEFAULT 'unknown'`},
		{"event_timezone", `ALTER TABLE entries ADD COLUMN event_timezone TEXT NOT NULL DEFAULT ''`},
		{"event_time_source", `ALTER TABLE entries ADD COLUMN event_time_source TEXT NOT NULL DEFAULT ''`},
		{"valid_from", `ALTER TABLE entries ADD COLUMN valid_from INTEGER`},
		{"valid_to", `ALTER TABLE entries ADD COLUMN valid_to INTEGER`},
		{"pinned_core", `ALTER TABLE entries ADD COLUMN pinned_core INTEGER NOT NULL DEFAULT 0`},
	} {
		if plan.have[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v3 add column %s: %w", col.name, err)
		}
	}

	// Segment B: fully idempotent DDL and backfills.
	for _, stmt := range v3Statements(fts) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("v3 schema: %w", err)
		}
	}
	return nil
}

func v3Statements(fts ftsPlan) []string {
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS ix_entries_valid ON entries(status, realm, valid_from, valid_to)`,
		`CREATE INDEX IF NOT EXISTS ix_entries_pinned ON entries(status, pinned_core) WHERE pinned_core=1`,

		// sources v3 keeps the v2 unique key so INSERT OR IGNORE still dedupes.
		`CREATE TABLE IF NOT EXISTS sources_v3 (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			entry_id         TEXT NOT NULL,
			conversation_id  TEXT NOT NULL DEFAULT '',
			session_key      TEXT NOT NULL DEFAULT '',
			message_id       INTEGER,
			source_id        TEXT NOT NULL DEFAULT '',
			source_type      TEXT NOT NULL DEFAULT 'explicit',
			evidence_text    TEXT NOT NULL DEFAULT '',
			extraction_model TEXT NOT NULL DEFAULT '',
			prompt_version   TEXT NOT NULL DEFAULT '',
			confidence       REAL NOT NULL DEFAULT 1.0,
			user_confirmed   INTEGER NOT NULL DEFAULT 0,
			created_at       INTEGER NOT NULL,
			UNIQUE(entry_id, source_type, source_id, session_key),
			FOREIGN KEY (entry_id) REFERENCES entries(id) ON DELETE CASCADE
		)`,
		`INSERT OR IGNORE INTO sources_v3 (
			entry_id, session_key, source_id, source_type, confidence, user_confirmed, created_at)
		 SELECT s.entry_id, s.session_key, s.source_id,
		        CASE s.source_type WHEN 'user_statement' THEN 'explicit' ELSE s.source_type END,
		        0.9, 0, s.created_at
		 FROM sources s
		 WHERE EXISTS (SELECT 1 FROM entries e WHERE e.id = s.entry_id)`,
		`INSERT OR IGNORE INTO meta(key,value) VALUES(
			'v3_dropped_orphan_sources',
			CAST((SELECT COUNT(*) FROM sources s
			      WHERE NOT EXISTS (SELECT 1 FROM entries e WHERE e.id=s.entry_id)) AS TEXT))`,
		`DROP TABLE IF EXISTS sources`,
		`ALTER TABLE sources_v3 RENAME TO sources`,
		`CREATE INDEX IF NOT EXISTS ix_sources_entry   ON sources(entry_id)`,
		`CREATE INDEX IF NOT EXISTS ix_sources_message ON sources(message_id) WHERE message_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS ix_sources_session ON sources(session_key) WHERE session_key <> ''`,

		`CREATE TABLE IF NOT EXISTS memory_relations (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id     TEXT NOT NULL,
			target_id     TEXT NOT NULL,
			relation_type TEXT NOT NULL,
			confidence    REAL NOT NULL DEFAULT 1.0,
			source_type   TEXT NOT NULL DEFAULT 'inferred',
			created_at    INTEGER NOT NULL,
			UNIQUE(source_id, target_id, relation_type),
			FOREIGN KEY (source_id) REFERENCES entries(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES entries(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_rel_source ON memory_relations(source_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_rel_target ON memory_relations(target_id)`,
		`INSERT OR IGNORE INTO memory_relations (source_id,target_id,relation_type,confidence,source_type,created_at)
		 SELECT s.entry_id, s.superseded_id, 'supersedes', 1.0, 'explicit', s.created_at
		 FROM supersedes s
		 WHERE EXISTS (SELECT 1 FROM entries e1 WHERE e1.id = s.entry_id)
		   AND EXISTS (SELECT 1 FROM entries e2 WHERE e2.id = s.superseded_id)`,
		`INSERT OR IGNORE INTO meta(key,value) VALUES(
			'v3_dropped_orphan_supersedes',
			CAST((SELECT COUNT(*) FROM supersedes s
			      WHERE NOT EXISTS (SELECT 1 FROM entries e WHERE e.id=s.superseded_id)
			         OR NOT EXISTS (SELECT 1 FROM entries e WHERE e.id=s.entry_id)) AS TEXT))`,

		`CREATE TABLE IF NOT EXISTS memory_clusters (
			id TEXT PRIMARY KEY, realm TEXT NOT NULL DEFAULT 'real', title TEXT NOT NULL,
			topic TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS memory_cluster_members (
			cluster_id TEXT NOT NULL, entry_id TEXT NOT NULL, ordinal INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (cluster_id, entry_id),
			FOREIGN KEY (cluster_id) REFERENCES memory_clusters(id) ON DELETE CASCADE,
			FOREIGN KEY (entry_id)   REFERENCES entries(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS memory_summary_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT, cluster_id TEXT NOT NULL, version INTEGER NOT NULL,
			summary TEXT NOT NULL, confidence REAL NOT NULL DEFAULT 0.8,
			model TEXT NOT NULL DEFAULT '', prompt_version TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active', created_at INTEGER NOT NULL, superseded_at INTEGER,
			UNIQUE(cluster_id, version),
			FOREIGN KEY (cluster_id) REFERENCES memory_clusters(id) ON DELETE CASCADE)`,
		`CREATE TABLE IF NOT EXISTS memory_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, operation TEXT NOT NULL,
			target_entry_id TEXT NOT NULL DEFAULT '',
			replacement_entry_id TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '', affected_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_ops_target ON memory_operations(target_entry_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_ops_hash ON memory_operations(content_hash) WHERE content_hash <> ''`,

		// Capsule rows are only inserted for entries that exist, so migrating a
		// different workspace cannot fail on a foreign key violation.
		`INSERT OR IGNORE INTO memory_clusters(id,realm,title,topic,status,created_at,updated_at) VALUES
		 ('capsule:fictional_t0','fictional','虚构写作 T0 铁律包','写作前置','active',
		  CAST(strftime('%s','now') AS INTEGER)*1000, CAST(strftime('%s','now') AS INTEGER)*1000),
		 ('capsule:fictional_t0_compact','fictional','虚构写作 T0 铁律包（紧凑版）','写作前置','active',
		  CAST(strftime('%s','now') AS INTEGER)*1000, CAST(strftime('%s','now') AS INTEGER)*1000)`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0', id, 1 FROM entries WHERE id='mem_b4_writing_card'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0', id, 2 FROM entries WHERE id='mem_b4_writing_boundary'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0', id, 3 FROM entries WHERE id='mem_b4_writing_direction'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0', id, 4 FROM entries WHERE id='mem_b4_essence'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0', id, 5 FROM entries WHERE id='mem_b4_writing_scale'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0_compact', id, 1 FROM entries WHERE id='mem_b4_writing_card'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0_compact', id, 2 FROM entries WHERE id='mem_b4_writing_boundary'`,
		`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal)
		 SELECT 'capsule:fictional_t0_compact', id, 4 FROM entries WHERE id='mem_b4_essence'`,
	}
	if !fts.available {
		return stmts
	}
	return append(stmts,
		`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
			subject, predicate, facet, value, text, tokenize="`+fts.tokenizer+`")`,
		`CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
			INSERT INTO entries_fts(rowid,subject,predicate,facet,value,text)
			VALUES (new.rowid,new.subject,new.predicate,new.facet,new.value,new.text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
			DELETE FROM entries_fts WHERE rowid = old.rowid;
		END`,
		// Only content columns: flushUsage updating use_count must not rebuild.
		`CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE OF text,subject,predicate,facet,value ON entries BEGIN
			DELETE FROM entries_fts WHERE rowid = old.rowid;
			INSERT INTO entries_fts(rowid,subject,predicate,facet,value,text)
			VALUES (new.rowid,new.subject,new.predicate,new.facet,new.value,new.text);
		END`,
		`INSERT OR IGNORE INTO entries_fts(rowid,subject,predicate,facet,value,text)
		 SELECT rowid,subject,predicate,facet,value,text FROM entries`,
	)
}

// MigrationStatus reports whether the database still needs schema or data
// migration. It is safe to call while the service is running.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	version := s.schemaVersionLocked(ctx)
	planV2 := s.planV2(ctx)
	tokenizer := s.metaValueLocked(ctx, "fts_tokenizer")
	fts := ftsPlan{available: tokenizer != "none", tokenizer: tokenizer}
	planV3 := s.planV3Columns(ctx)

	st := MigrationStatus{
		SchemaVersion:        version,
		CurrentVersion:       CurrentSchemaVersion,
		NeedsSchemaMigration: version < CurrentSchemaVersion || !planV2.complete() || !s.v3CompleteLocked(ctx, fts) || !planV3.have["pinned_core"],
		DBPath:               s.path,
		Backup:               s.metaValueLocked(ctx, "last_migration_backup"),
		FTSTokenizer:         tokenizer,
	}
	if raw := s.metaValueLocked(ctx, "last_migration_at"); raw != "" {
		_, _ = fmt.Sscanf(raw, "%d", &st.LastMigrationAt)
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&st.Total)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE status='active'`).Scan(&st.Active)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`).Scan(&st.EmptyPredicate)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE status='active' AND layer='relationship' AND TRIM(facet)=''`).
		Scan(&st.EmptyRelationshipFacet)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE status='active' AND layer='relationship'`).Scan(&st.Relationship)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM personas`).Scan(&st.Personas)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE pinned_core=1`).Scan(&st.PinnedCore)
	if s.tableExistsLocked(ctx, "memory_relations") {
		st.Relations, _ = s.countLocked(ctx, "memory_relations")
	}
	if s.tableExistsLocked(ctx, "entries_fts") {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&st.FTSRows)
	}
	if info, err := os.Stat(s.path); err == nil {
		st.DBSize = info.Size()
	}
	st.NeedsDataMigration = st.EmptyPredicate > 0
	st.NeedsMigration = st.NeedsSchemaMigration || st.NeedsDataMigration
	return st, nil
}

func (s *Store) countLocked(ctx context.Context, table string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n)
	return n, err
}

// ApplyMigration upgrades v1/v2 databases to v3 and applies the idempotent
// data repairs. It never deletes entries and never changes status except for
// the explicit birthday restore.
func (s *Store) ApplyMigration(ctx context.Context) (MigrationReport, error) {
	previous := s.openedSchema
	if previous == 0 {
		previous = s.schemaVersionLocked(ctx)
	}
	if err := s.migrateSchema(ctx); err != nil {
		return MigrationReport{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	backup, err := s.backupWithRotation(ctx, "repair")
	if err != nil {
		return MigrationReport{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MigrationReport{}, err
	}
	defer func() { _ = tx.Rollback() }()

	report := MigrationReport{PreviousSchemaVersion: previous}
	now := time.Now().UnixMilli()

	if err := s.repairDataLocked(ctx, tx, now, &report); err != nil {
		return MigrationReport{}, err
	}

	if err := s.setMetaLocked(ctx, tx, "v3_repair_at", fmt.Sprintf("%d", now)); err != nil {
		return MigrationReport{}, err
	}
	if err := s.setMetaLocked(ctx, tx, "last_migration_backup", backup); err != nil {
		return MigrationReport{}, err
	}
	if err := s.setMetaLocked(ctx, tx, "last_migration_at", fmt.Sprintf("%d", now)); err != nil {
		return MigrationReport{}, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return MigrationReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return MigrationReport{}, err
	}

	report.SchemaVersion = s.schemaVersionLocked(ctx)
	report.Backup = backup
	report.FTSTokenizer = s.metaValueLocked(ctx, "fts_tokenizer")
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&report.Total)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE status='active'`).Scan(&report.Active)
	if report.FTSTokenizer != "none" {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&report.FTSRows)
	}
	return report, nil
}

// repairDataLocked applies the idempotent v3 data repairs (R1-R4) and records
// the orphan counts produced by the schema migration.
func (s *Store) repairDataLocked(ctx context.Context, tx *sql.Tx, now int64, report *MigrationReport) error {
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`).Scan(&report.EmptyPredicate); err != nil {
		return err
	}

	for id, mapping := range relationshipFacetMappings {
		res, err := tx.ExecContext(ctx,
			`UPDATE entries SET predicate=?, facet=?, updated_at=?
			 WHERE id=? AND status='active'
			   AND (TRIM(predicate)='' OR predicate='待分类' OR TRIM(facet)='')`,
			mapping.predicate, mapping.facet, now, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		report.MappedRelationships += int(n)
	}
	for id, mapping := range v3PredicateMappings {
		res, err := tx.ExecContext(ctx,
			`UPDATE entries SET predicate=?, facet=?, updated_at=?
			 WHERE id=? AND predicate='待分类'`,
			mapping.predicate, mapping.facet, now, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		report.MappedPredicates += int(n)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE entries SET predicate='待分类', updated_at=? WHERE TRIM(predicate)=''`, now)
	if err != nil {
		return err
	}
	updated, _ := res.RowsAffected()
	report.UpdatedPredicates = int(updated) + report.MappedRelationships + report.MappedPredicates

	// R2: reclassify dialogue that was stored as a real profile fact.
	res, err = tx.ExecContext(ctx,
		`UPDATE entries SET realm='fictional', layer='procedural', audience='private',
		 predicate='场景', facet='开局宣告台词·演员台词', importance=0.55, updated_at=?
		 WHERE id=? AND realm='real' AND layer='profile'`, now, v3PollutionEntry)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		report.Reclassified = int(n)
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO sources(entry_id,source_type,source_id,session_key,confidence,user_confirmed,created_at)
			 VALUES(?,'reclassified','repair_v3','',1.0,1,?)`, v3PollutionEntry, now); err != nil {
			return err
		}
	}

	// R3: restore the birthday entry. The date is derived from the text, never
	// hardcoded in the repository.
	res, err = tx.ExecContext(ctx,
		`UPDATE entries SET status='active', predicate='birthday', facet='日期',
		 value=CASE WHEN instr(text,'是')>0 THEN substr(text, instr(text,'是')+1) ELSE value END,
		 updated_at=? WHERE id=? AND status='archived'`, now, v3BirthdayEntry)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		report.Restored = int(n)
	}

	// R4: every entry gets at least one evidence source.
	res, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO sources(entry_id,source_type,source_id,session_key,confidence,user_confirmed,created_at)
		 SELECT e.id,'imported','b4_batch_consolidation_20261031','',0.7,0,e.created_at
		 FROM entries e
		 WHERE e.id LIKE 'mem_b4_%'
		   AND NOT EXISTS (SELECT 1 FROM sources s WHERE s.entry_id=e.id)`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		report.BackfilledSources = int(n)
	}
	res, err = tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO sources(entry_id,source_type,source_id,session_key,confidence,user_confirmed,created_at)
		 SELECT ?, 'manual', 'manual_edit_20261031', '', 1.0, 1, ?
		 WHERE EXISTS (SELECT 1 FROM entries WHERE id=?)`, v3ManualSourceEntry, now, v3ManualSourceEntry)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		report.BackfilledSources++
	}

	for key, dst := range map[string]*int{
		"v3_dropped_orphan_sources":    &report.DroppedOrphanSources,
		"v3_dropped_orphan_supersedes": &report.DroppedOrphanSupersedes,
	} {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&raw); err == nil {
			_, _ = fmt.Sscanf(raw, "%d", dst)
		}
	}

	return nil
}

// ApplyV2Migration is kept for source compatibility. It now runs the full
// v1 → v2 → v3 migration chain.
func (s *Store) ApplyV2Migration(ctx context.Context) (MigrationReport, error) {
	return s.ApplyMigration(ctx)
}

// RepairPreviewData reports how many rows the idempotent data repair would
// touch. It does not write anything.
func (s *Store) RepairPreviewData(ctx context.Context) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	queries := map[string]string{
		"empty_predicate":      `SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`,
		"uncategorized_active": `SELECT COUNT(*) FROM entries WHERE status='active' AND predicate='待分类'`,
		"pollution_real":       `SELECT COUNT(*) FROM entries WHERE id='` + v3PollutionEntry + `' AND realm='real' AND layer='profile'`,
		"birthday_archived":    `SELECT COUNT(*) FROM entries WHERE id='` + v3BirthdayEntry + `' AND status='archived'`,
		"unsourced":            `SELECT COUNT(*) FROM entries e WHERE NOT EXISTS (SELECT 1 FROM sources s WHERE s.entry_id=e.id)`,
	}
	for key, q := range queries {
		var n int
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}

// RepairData applies only the idempotent data repairs, without a schema
// upgrade. It is the `picoclaw memory repair` entry point.
func (s *Store) RepairData(ctx context.Context) (MigrationReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	backup, err := s.backupWithRotation(ctx, "repair")
	if err != nil {
		return MigrationReport{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MigrationReport{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UnixMilli()
	report := MigrationReport{PreviousSchemaVersion: s.schemaVersionLocked(ctx), Backup: backup}
	if err := s.repairDataLocked(ctx, tx, now, &report); err != nil {
		return MigrationReport{}, err
	}
	if err := s.setMetaLocked(ctx, tx, "last_migration_backup", backup); err != nil {
		return MigrationReport{}, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return MigrationReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return MigrationReport{}, err
	}
	report.SchemaVersion = s.schemaVersionLocked(ctx)
	return report, nil
}

// BackupCount returns how many rotated backups exist.
func (s *Store) BackupCount() int {
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.path), "backups", "memory.db.bak-*"))
	return len(files)
}

// Tokenizer returns the FTS tokenizer recorded in meta.
func (s *Store) Tokenizer(ctx context.Context) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.metaValueLocked(ctx, "fts_tokenizer"))
}
