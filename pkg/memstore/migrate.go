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
//
// PicoClaw 0.7 migrates v3 databases to v4 and nothing else: the 0.4/0.5
// migration chain (v1 → v2 → v3) was removed on purpose. A database that is
// still on v1 or v2 must be opened once with a 0.6.x build, which upgrades it
// to v3; this build then migrates v3 → v4 without replaying old segments.
const CurrentSchemaVersion = 4

// MinMigratableSchemaVersion is the oldest schema this build accepts.
const MinMigratableSchemaVersion = 3

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

	// V4 additions.
	Domains      int `json:"domains"`
	OriginEvents int `json:"origin_events"`
	Jobs         int `json:"jobs"`
	Dependencies int `json:"dependencies"`
}

// MigrationReport is returned after applying the migration chain.
type MigrationReport struct {
	PreviousSchemaVersion int    `json:"previous_schema_version"`
	SchemaVersion         int    `json:"schema_version"`
	Total                 int    `json:"total"`
	Active                int    `json:"active"`
	Backup                string `json:"backup"`
	FTSTokenizer          string `json:"fts_tokenizer"`
	FTSRows               int    `json:"fts_rows"`

	// Legacy field names stay for the dashboard that still renders them; the
	// v3 data-repair segments no longer run, so they are always zero.
	EmptyPredicate          int `json:"empty_predicate"`
	UpdatedPredicates       int `json:"updated_predicates"`
	MappedRelationships     int `json:"mapped_relationships"`
	MappedPredicates        int `json:"mapped_predicates"`
	Reclassified            int `json:"reclassified"`
	Restored                int `json:"restored"`
	BackfilledSources       int `json:"backfilled_sources"`
	DroppedOrphanSources    int `json:"dropped_orphan_sources"`
	DroppedOrphanSupersedes int `json:"dropped_orphan_supersedes"`

	// V4 additions.
	SourcesRebuilt    int `json:"sources_rebuilt"`
	DomainsCreated    int `json:"domains_created"`
	EntriesWithDomain int `json:"entries_with_domain"`
	JobsScheduled     int `json:"jobs_scheduled"`
}

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

// tableSQLLocked returns the CREATE statement recorded in sqlite_master.
func (s *Store) tableSQLLocked(ctx context.Context, table string) string {
	var sqlText string
	if err := s.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&sqlText); err != nil {
		return ""
	}
	return sqlText
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

type ftsPlan struct {
	available bool
	tokenizer string
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

func (s *Store) ftsCompleteLocked(ctx context.Context, fts ftsPlan) bool {
	if !fts.available {
		return true
	}
	return s.tableExistsLocked(ctx, "entries_fts") &&
		s.triggerExistsLocked(ctx, "entries_ai") &&
		s.triggerExistsLocked(ctx, "entries_ad") &&
		s.triggerExistsLocked(ctx, "entries_au")
}

// v4Tables are the tables that must exist in a v4 database. Tables that
// already existed in v3 are included so a partially healed database is
// repaired by the same idempotent segment.
var v4Tables = []string{
	"memory_relations",
	"memory_clusters",
	"memory_cluster_members",
	"memory_summary_versions",
	"memory_operations",
	"memory_domains",
	"memory_actor_bindings",
	"memory_processing_grants",
	"memory_origin_events",
	"memory_dependencies",
	"memory_jobs",
	"memory_change_plans",
	"memory_commitments",
	"memory_recalls",
	"memory_recall_feedback",
	"memory_summary_evidence",
}

// v3EntryColumns are the columns added by the removed v3 segment. They are
// still verified here so a v3 database that lost a column (interrupted
// upgrade) is healed by the v4 segment instead of writing to a broken table.
var v3EntryColumns = []string{
	"event_time_precision", "event_timezone", "event_time_source",
	"valid_from", "valid_to", "pinned_core",
}

// v4EntryColumns are the columns added by v4.
var v4EntryColumns = []string{
	"domain_id", "row_revision", "archived_from", "event_start", "event_end",
	"sensitivity", "qualifiers", "qualifiers_hash", "evidence_status", "freshness",
}

var v4RelationshipColumns = []string{"domain_id", "realm", "audience", "source_event_id"}
var v4RelationshipHistoryColumns = []string{"domain_id", "realm", "audience", "source_event_id"}
var v4OperationColumns = []string{"request_id", "actor_ref", "reason_code", "policy_revision"}
var v4ClusterColumns = []string{"domain_id", "generation", "stage_start", "stage_end"}
var v4SummaryColumns = []string{"domain_id", "generation", "input_fingerprint", "claims", "review_status", "invalidated_at"}

// v4Plan is computed before BeginTx: Store uses MaxOpenConns(1), so any s.db
// query while the transaction holds the connection would deadlock.
type v4Plan struct {
	entryCols        map[string]bool
	relCols          map[string]bool
	relHistCols      map[string]bool
	opCols           map[string]bool
	clusterCols      map[string]bool
	summaryCols      map[string]bool
	sourceCols       map[string]bool
	sourcesHasOrigin bool
	missingTables    map[string]bool
}

func (s *Store) planV4(ctx context.Context) v4Plan {
	p := v4Plan{
		entryCols:     map[string]bool{},
		relCols:       map[string]bool{},
		relHistCols:   map[string]bool{},
		opCols:        map[string]bool{},
		clusterCols:   map[string]bool{},
		summaryCols:   map[string]bool{},
		sourceCols:    map[string]bool{},
		missingTables: map[string]bool{},
	}
	for _, c := range append(append([]string{}, v3EntryColumns...), v4EntryColumns...) {
		p.entryCols[c] = s.hasColumnLocked(ctx, "entries", c)
	}
	for _, c := range v4RelationshipColumns {
		p.relCols[c] = s.hasColumnLocked(ctx, "relationship_state", c)
	}
	for _, c := range v4RelationshipHistoryColumns {
		p.relHistCols[c] = s.hasColumnLocked(ctx, "relationship_state_history", c)
	}
	for _, c := range v4OperationColumns {
		p.opCols[c] = s.hasColumnLocked(ctx, "memory_operations", c)
	}
	for _, c := range v4ClusterColumns {
		p.clusterCols[c] = s.hasColumnLocked(ctx, "memory_clusters", c)
	}
	for _, c := range v4SummaryColumns {
		p.summaryCols[c] = s.hasColumnLocked(ctx, "memory_summary_versions", c)
	}
	for _, c := range []string{
		"entry_id", "conversation_id", "session_key", "message_id", "source_id",
		"source_type", "evidence_text", "extraction_model", "prompt_version",
		"confidence", "user_confirmed", "created_at",
	} {
		p.sourceCols[c] = s.hasColumnLocked(ctx, "sources", c)
	}
	p.sourcesHasOrigin = strings.Contains(s.tableSQLLocked(ctx, "sources"), "origin_event_id")
	for _, t := range v4Tables {
		p.missingTables[t] = !s.tableExistsLocked(ctx, t)
	}
	return p
}

func (p v4Plan) complete() bool {
	for _, cols := range []map[string]bool{
		p.entryCols, p.relCols, p.relHistCols, p.opCols, p.clusterCols, p.summaryCols,
	} {
		for _, ok := range cols {
			if !ok {
				return false
			}
		}
	}
	for _, ok := range p.sourceCols {
		if !ok {
			return false
		}
	}
	if !p.sourcesHasOrigin {
		return false
	}
	for _, missing := range p.missingTables {
		if missing {
			return false
		}
	}
	return true
}

// migrateSchema upgrades an existing v3 database to v4. It is idempotent and
// safe to call on every Open. Databases older than v3 are rejected: the old
// v1 → v2 → v3 chain is intentionally gone.
func (s *Store) migrateSchema(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.migrateSchemaLocked(ctx)
	return err
}

func (s *Store) migrateSchemaLocked(ctx context.Context) (int, string, error) {
	previous := s.schemaVersionLocked(ctx)
	if previous > CurrentSchemaVersion {
		return previous, "", fmt.Errorf(
			"memstore: database schema v%d is newer than this build (v%d); upgrade PicoClaw before using it",
			previous, CurrentSchemaVersion)
	}
	if previous < MinMigratableSchemaVersion {
		return previous, "", fmt.Errorf(
			"memstore: database schema v%d is not supported by this build; run a 0.6.x build once to upgrade it to v3, then retry",
			previous)
	}

	plan := s.planV4(ctx)
	fts := s.detectFTS(ctx)
	if plan.complete() && previous >= CurrentSchemaVersion && s.ftsCompleteLocked(ctx, fts) {
		return previous, "", nil
	}

	backup := ""
	needBackup := previous < CurrentSchemaVersion
	if needBackup {
		var err error
		backup, err = s.backupWithRotation(ctx, "schema-v4")
		if err != nil {
			return previous, "", err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return previous, backup, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.migrateToV4Locked(ctx, tx, plan, fts); err != nil {
		return previous, backup, err
	}

	// The version is written only after the whole segment succeeded.
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

// migrateToV4Locked is the single v3 → v4 schema segment. Every statement is
// guarded by the precomputed plan so the segment can be re-run on a partially
// healed database. Sources are rebuilt field by field: the old v3 segment is
// gone, and losing evidence columns was exactly the B05 defect this avoids.
func (s *Store) migrateToV4Locked(ctx context.Context, tx *sql.Tx, plan v4Plan, fts ftsPlan) error {
	now := time.Now().UnixMilli()

	// 0. Create the v4 tables first. On a fresh database the ALTERs below then
	// add the governance columns; on a v3 database this is a no-op and the
	// ALTERs do the upgrade.
	for _, stmt := range v4Statements() {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("v4 schema: %w", err)
		}
	}

	// 1. entries: v3 base columns first (heal), then v4 governance columns.
	for _, col := range []struct{ name, ddl string }{
		{"event_time_precision", `ALTER TABLE entries ADD COLUMN event_time_precision TEXT NOT NULL DEFAULT 'unknown'`},
		{"event_timezone", `ALTER TABLE entries ADD COLUMN event_timezone TEXT NOT NULL DEFAULT ''`},
		{"event_time_source", `ALTER TABLE entries ADD COLUMN event_time_source TEXT NOT NULL DEFAULT ''`},
		{"valid_from", `ALTER TABLE entries ADD COLUMN valid_from INTEGER`},
		{"valid_to", `ALTER TABLE entries ADD COLUMN valid_to INTEGER`},
		{"pinned_core", `ALTER TABLE entries ADD COLUMN pinned_core INTEGER NOT NULL DEFAULT 0`},
		{"domain_id", `ALTER TABLE entries ADD COLUMN domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned'`},
		{"row_revision", `ALTER TABLE entries ADD COLUMN row_revision INTEGER NOT NULL DEFAULT 1`},
		{"archived_from", `ALTER TABLE entries ADD COLUMN archived_from TEXT NOT NULL DEFAULT ''`},
		{"event_start", `ALTER TABLE entries ADD COLUMN event_start INTEGER`},
		{"event_end", `ALTER TABLE entries ADD COLUMN event_end INTEGER`},
		{"sensitivity", `ALTER TABLE entries ADD COLUMN sensitivity TEXT NOT NULL DEFAULT 'normal'`},
		{"qualifiers", `ALTER TABLE entries ADD COLUMN qualifiers TEXT NOT NULL DEFAULT ''`},
		{"qualifiers_hash", `ALTER TABLE entries ADD COLUMN qualifiers_hash TEXT NOT NULL DEFAULT ''`},
		{"evidence_status", `ALTER TABLE entries ADD COLUMN evidence_status TEXT NOT NULL DEFAULT 'supported'`},
		{"freshness", `ALTER TABLE entries ADD COLUMN freshness TEXT NOT NULL DEFAULT 'unknown'`},
	} {
		if plan.entryCols[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v4 add entries.%s: %w", col.name, err)
		}
	}

	// 2. relationship state and history: domain/audience scoping.
	for _, col := range []struct{ name, ddl string }{
		{"domain_id", `ALTER TABLE relationship_state ADD COLUMN domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned'`},
		{"realm", `ALTER TABLE relationship_state ADD COLUMN realm TEXT NOT NULL DEFAULT ''`},
		{"audience", `ALTER TABLE relationship_state ADD COLUMN audience TEXT NOT NULL DEFAULT ''`},
		{"source_event_id", `ALTER TABLE relationship_state ADD COLUMN source_event_id TEXT NOT NULL DEFAULT ''`},
	} {
		if plan.relCols[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v4 add relationship_state.%s: %w", col.name, err)
		}
	}
	for _, col := range []struct{ name, ddl string }{
		{"domain_id", `ALTER TABLE relationship_state_history ADD COLUMN domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned'`},
		{"realm", `ALTER TABLE relationship_state_history ADD COLUMN realm TEXT NOT NULL DEFAULT ''`},
		{"audience", `ALTER TABLE relationship_state_history ADD COLUMN audience TEXT NOT NULL DEFAULT ''`},
		{"source_event_id", `ALTER TABLE relationship_state_history ADD COLUMN source_event_id TEXT NOT NULL DEFAULT ''`},
	} {
		if plan.relHistCols[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v4 add relationship_state_history.%s: %w", col.name, err)
		}
	}

	// 3. Operation log: request identity and policy revision.
	for _, col := range []struct{ name, ddl string }{
		{"request_id", `ALTER TABLE memory_operations ADD COLUMN request_id TEXT NOT NULL DEFAULT ''`},
		{"actor_ref", `ALTER TABLE memory_operations ADD COLUMN actor_ref TEXT NOT NULL DEFAULT ''`},
		{"reason_code", `ALTER TABLE memory_operations ADD COLUMN reason_code TEXT NOT NULL DEFAULT ''`},
		{"policy_revision", `ALTER TABLE memory_operations ADD COLUMN policy_revision INTEGER NOT NULL DEFAULT 0`},
	} {
		if plan.opCols[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v4 add memory_operations.%s: %w", col.name, err)
		}
	}

	// 4. Cluster and summary versions: stage/generation bookkeeping.
	for _, col := range []struct{ name, ddl string }{
		{"domain_id", `ALTER TABLE memory_clusters ADD COLUMN domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned'`},
		{"generation", `ALTER TABLE memory_clusters ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`},
		{"stage_start", `ALTER TABLE memory_clusters ADD COLUMN stage_start INTEGER`},
		{"stage_end", `ALTER TABLE memory_clusters ADD COLUMN stage_end INTEGER`},
	} {
		if plan.clusterCols[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v4 add memory_clusters.%s: %w", col.name, err)
		}
	}
	for _, col := range []struct{ name, ddl string }{
		{"domain_id", `ALTER TABLE memory_summary_versions ADD COLUMN domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned'`},
		{"generation", `ALTER TABLE memory_summary_versions ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`},
		{"input_fingerprint", `ALTER TABLE memory_summary_versions ADD COLUMN input_fingerprint TEXT NOT NULL DEFAULT ''`},
		{"claims", `ALTER TABLE memory_summary_versions ADD COLUMN claims TEXT NOT NULL DEFAULT '[]'`},
		{"review_status", `ALTER TABLE memory_summary_versions ADD COLUMN review_status TEXT NOT NULL DEFAULT 'approved'`},
		{"invalidated_at", `ALTER TABLE memory_summary_versions ADD COLUMN invalidated_at INTEGER`},
	} {
		if plan.summaryCols[col.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("v4 add memory_summary_versions.%s: %w", col.name, err)
		}
	}

	// 5. Rebuild sources with the origin-event identity, preserving every v3
	// evidence field instead of projecting onto a reduced column set (B05).
	if !plan.sourcesHasOrigin {
		if err := rebuildSourcesV4(ctx, tx, plan, now); err != nil {
			return err
		}
	}

	// 6. Default isolated domain for unclaimed legacy data. Migration never
	// guesses an owner from scope, session or text.
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO memory_domains
		 (domain_id,owner_principal_id,scope_key,status,policy_epoch,created_at,updated_at)
		 VALUES('legacy_unassigned','','','active',1,?,?)`, now, now); err != nil {
		return fmt.Errorf("v4 default domain: %w", err)
	}

	// 8. Indexes: the exact-fingerprint uniqueness is now per domain (FR-03).
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS ux_entries_hash`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_entries_hash ON entries(domain_id, content_hash) WHERE status='active'`,
		`CREATE INDEX IF NOT EXISTS ix_entries_domain ON entries(domain_id, status, realm, layer)`,
		`CREATE INDEX IF NOT EXISTS ix_entries_revision ON entries(row_revision)`,
		`CREATE INDEX IF NOT EXISTS ix_entries_facet ON entries(realm, subject, predicate, facet, status)`,
		`CREATE INDEX IF NOT EXISTS ix_entries_valid ON entries(status, realm, valid_from, valid_to)`,
		`CREATE INDEX IF NOT EXISTS ix_entries_pinned ON entries(status, pinned_core) WHERE pinned_core=1`,
		`CREATE INDEX IF NOT EXISTS ix_sources_entry ON sources(entry_id)`,
		`CREATE INDEX IF NOT EXISTS ix_sources_message ON sources(message_id) WHERE message_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS ix_sources_session ON sources(session_key) WHERE session_key <> ''`,
		`CREATE INDEX IF NOT EXISTS ix_sources_origin ON sources(origin_event_id) WHERE origin_event_id <> ''`,
		`CREATE INDEX IF NOT EXISTS ix_memory_ops_request ON memory_operations(request_id) WHERE request_id <> ''`,
		`CREATE INDEX IF NOT EXISTS ix_personas_active ON personas(active, realm, audience)`,
		`CREATE INDEX IF NOT EXISTS ix_origin_events_session ON memory_origin_events(session_key, message_id)`,
		`CREATE INDEX IF NOT EXISTS ix_origin_events_domain ON memory_origin_events(domain_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_dep_child ON memory_dependencies(child_kind, child_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_dep_parent ON memory_dependencies(parent_kind, parent_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_jobs_state ON memory_jobs(state, priority, next_run_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_memory_recalls_dedup ON memory_recalls(dedup_key) WHERE dedup_key <> ''`,
		`CREATE INDEX IF NOT EXISTS ix_memory_recalls_domain ON memory_recalls(domain_id, status)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("v4 index: %w", err)
		}
	}

	// 9. FTS is a derived index: rebuild it when missing, never if disabled.
	if fts.available {
		for _, stmt := range []string{
			`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
				subject, predicate, facet, value, text, tokenize="` + fts.tokenizer + `")`,
			`CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
				INSERT INTO entries_fts(rowid,subject,predicate,facet,value,text)
				VALUES (new.rowid,new.subject,new.predicate,new.facet,new.value,new.text);
			END`,
			`CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
				DELETE FROM entries_fts WHERE rowid = old.rowid;
			END`,
			`CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE OF text,subject,predicate,facet,value ON entries BEGIN
				DELETE FROM entries_fts WHERE rowid = old.rowid;
				INSERT INTO entries_fts(rowid,subject,predicate,facet,value,text)
				VALUES (new.rowid,new.subject,new.predicate,new.facet,new.value,new.text);
			END`,
			`INSERT OR IGNORE INTO entries_fts(rowid,subject,predicate,facet,value,text)
			 SELECT rowid,subject,predicate,facet,value,text FROM entries`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("v4 fts: %w", err)
			}
		}
	}
	return nil
}

// rebuildSourcesV4 copies the old sources table into the v4 shape. Columns
// missing in the old table get explicit defaults; nothing that exists is
// dropped. Orphan rows never enter the new table.
func rebuildSourcesV4(ctx context.Context, tx *sql.Tx, plan v4Plan, now int64) error {
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS sources_v4`); err != nil {
		return fmt.Errorf("v4 drop sources_v4: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE sources_v4 (
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
		origin_event_id  TEXT NOT NULL DEFAULT '',
		origin_revision  INTEGER NOT NULL DEFAULT 1,
		confirmation_event_id TEXT NOT NULL DEFAULT '',
		evidence_root    TEXT NOT NULL DEFAULT '',
		created_at       INTEGER NOT NULL,
		UNIQUE(entry_id, source_type, source_id, session_key, origin_event_id),
		FOREIGN KEY (entry_id) REFERENCES entries(id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("v4 create sources_v4: %w", err)
	}

	cols := []string{
		"entry_id", "conversation_id", "session_key", "message_id", "source_id",
		"source_type", "evidence_text", "extraction_model", "prompt_version",
		"confidence", "user_confirmed",
	}
	defaults := map[string]string{
		"conversation_id":  "''",
		"session_key":      "''",
		"message_id":       "NULL",
		"source_id":        "''",
		"source_type":      "'explicit'",
		"evidence_text":    "''",
		"extraction_model": "''",
		"prompt_version":   "''",
		"confidence":       "0.9",
		"user_confirmed":   "0",
	}
	selects := make([]string, len(cols))
	for i, c := range cols {
		switch {
		case !plan.sourceCols[c]:
			selects[i] = defaults[c]
		case c == "source_type":
			// v1/v2 used user_statement for the same evidence grade.
			selects[i] = `CASE source_type WHEN 'user_statement' THEN 'explicit' ELSE source_type END`
		default:
			selects[i] = c
		}
	}
	createdAt := "created_at"
	if !plan.sourceCols["created_at"] {
		createdAt = fmt.Sprintf("%d", now)
	}
	insert := `INSERT INTO sources_v4 (` + strings.Join(cols, ",") + `,created_at)
		SELECT ` + strings.Join(selects, ",") + `,` + createdAt + `
		FROM sources
		WHERE EXISTS (SELECT 1 FROM entries e WHERE e.id = sources.entry_id)`
	if _, err := tx.ExecContext(ctx, insert); err != nil {
		return fmt.Errorf("v4 copy sources: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE sources`); err != nil {
		return fmt.Errorf("v4 drop sources: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE sources_v4 RENAME TO sources`); err != nil {
		return fmt.Errorf("v4 rename sources: %w", err)
	}
	return nil
}

// v4Statements creates the v4 tables. They are all IF NOT EXISTS so the
// segment can heal a database where a table was dropped by hand or by an
// interrupted upgrade.
func v4Statements() []string {
	return []string{
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
			FOREIGN KEY (target_id) REFERENCES entries(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_rel_source ON memory_relations(source_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_rel_target ON memory_relations(target_id)`,

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
		`CREATE TABLE IF NOT EXISTS memory_summary_evidence (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			summary_version_id INTEGER NOT NULL,
			claim_id TEXT NOT NULL,
			entry_id TEXT NOT NULL,
			source_id INTEGER NOT NULL DEFAULT 0,
			row_revision INTEGER NOT NULL DEFAULT 0,
			evidence_span TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			UNIQUE(summary_version_id, claim_id, entry_id),
			FOREIGN KEY (summary_version_id) REFERENCES memory_summary_versions(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS ix_summary_evidence_entry ON memory_summary_evidence(entry_id)`,

		`CREATE TABLE IF NOT EXISTS memory_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, operation TEXT NOT NULL,
			target_entry_id TEXT NOT NULL DEFAULT '',
			replacement_entry_id TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '', affected_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_ops_target ON memory_operations(target_entry_id)`,
		`CREATE INDEX IF NOT EXISTS ix_memory_ops_hash ON memory_operations(content_hash) WHERE content_hash <> ''`,

		// Owner and scope partitions. Unknown ownership is isolated, never
		// guessed from text or session strings.
		`CREATE TABLE IF NOT EXISTS memory_domains (
			domain_id TEXT PRIMARY KEY,
			owner_principal_id TEXT NOT NULL DEFAULT '',
			scope_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			policy_epoch INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS memory_actor_bindings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			canonical_actor TEXT NOT NULL,
			owner_principal_id TEXT NOT NULL,
			channel_scope TEXT NOT NULL DEFAULT '',
			binding_revision INTEGER NOT NULL DEFAULT 1,
			revoked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(canonical_actor, owner_principal_id))`,
		`CREATE INDEX IF NOT EXISTS ix_actor_bindings_owner ON memory_actor_bindings(owner_principal_id, revoked)`,

		// Processing grants bind purpose + recipient, not just "cloud allowed".
		`CREATE TABLE IF NOT EXISTS memory_processing_grants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_principal_id TEXT NOT NULL DEFAULT '',
			domain_id TEXT NOT NULL DEFAULT '',
			purpose TEXT NOT NULL,
			recipient_ref TEXT NOT NULL,
			data_scope TEXT NOT NULL DEFAULT 'long_term',
			policy_revision INTEGER NOT NULL DEFAULT 0,
			revoked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(owner_principal_id, domain_id, purpose, recipient_ref))`,

		// Logical message identity shared by JSONL and Seahorse.
		`CREATE TABLE IF NOT EXISTS memory_origin_events (
			event_id TEXT PRIMARY KEY,
			origin_revision INTEGER NOT NULL DEFAULT 1,
			source_backend TEXT NOT NULL DEFAULT 'jsonl',
			source_locator TEXT NOT NULL DEFAULT '',
			session_key TEXT NOT NULL DEFAULT '',
			message_id TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			available INTEGER NOT NULL DEFAULT 1,
			event_at INTEGER,
			capture_policy_version INTEGER NOT NULL DEFAULT 1,
			domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned',
			created_at INTEGER NOT NULL)`,

		// Reverse dependencies keep derived views revocable.
		`CREATE TABLE IF NOT EXISTS memory_dependencies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			parent_kind TEXT NOT NULL,
			parent_id TEXT NOT NULL,
			parent_revision INTEGER NOT NULL DEFAULT 0,
			child_kind TEXT NOT NULL,
			child_id TEXT NOT NULL,
			dependency_type TEXT NOT NULL DEFAULT 'derived_from',
			created_at INTEGER NOT NULL,
			UNIQUE(parent_kind, parent_id, child_kind, child_id, dependency_type))`,

		// Durable job queue for extraction, summary and cleanup work.
		`CREATE TABLE IF NOT EXISTS memory_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL UNIQUE,
			job_kind TEXT NOT NULL,
			input_fingerprint TEXT NOT NULL DEFAULT '',
			generation INTEGER NOT NULL DEFAULT 0,
			policy_revision INTEGER NOT NULL DEFAULT 0,
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_until INTEGER,
			state TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0,
			next_run_at INTEGER,
			priority INTEGER NOT NULL DEFAULT 5,
			last_error TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL)`,

		// One-shot approval plans: the model can request, only the owner approves.
		`CREATE TABLE IF NOT EXISTS memory_change_plans (
			plan_id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			target_kind TEXT NOT NULL DEFAULT 'entry',
			target_id TEXT NOT NULL DEFAULT '',
			expected_revision INTEGER NOT NULL DEFAULT 0,
			params_digest TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			approved_by TEXT NOT NULL DEFAULT '',
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			executed_at INTEGER)`,

		`CREATE TABLE IF NOT EXISTS memory_commitments (
			id TEXT PRIMARY KEY,
			domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned',
			text TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'proposed',
			due_at INTEGER,
			due_timezone TEXT NOT NULL DEFAULT '',
			fulfilled_by TEXT NOT NULL DEFAULT '',
			source_event_id TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL)`,

		`CREATE TABLE IF NOT EXISTS memory_recalls (
			id TEXT PRIMARY KEY,
			domain_id TEXT NOT NULL DEFAULT 'legacy_unassigned',
			topic TEXT NOT NULL DEFAULT '',
			entry_ids TEXT NOT NULL DEFAULT '',
			target_ref TEXT NOT NULL DEFAULT '',
			dedup_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'candidate',
			trigger_kind TEXT NOT NULL DEFAULT 'commitment',
			scheduled_at INTEGER,
			sent_at INTEGER,
			created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS memory_recall_feedback (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			recall_id TEXT NOT NULL,
			feedback TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			FOREIGN KEY (recall_id) REFERENCES memory_recalls(id) ON DELETE CASCADE)`,
	}
}

// MigrationStatus reports whether the database still needs schema or data
// migration. It is safe to call while the service is running.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	version := s.schemaVersionLocked(ctx)
	tokenizer := s.metaValueLocked(ctx, "fts_tokenizer")
	fts := ftsPlan{available: tokenizer != "none", tokenizer: tokenizer}
	plan := s.planV4(ctx)

	st := MigrationStatus{
		SchemaVersion:        version,
		CurrentVersion:       CurrentSchemaVersion,
		NeedsSchemaMigration: version != CurrentSchemaVersion || !plan.complete() || !s.ftsCompleteLocked(ctx, fts),
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
	if s.tableExistsLocked(ctx, "memory_domains") {
		st.Domains, _ = s.countLocked(ctx, "memory_domains")
	}
	if s.tableExistsLocked(ctx, "memory_origin_events") {
		st.OriginEvents, _ = s.countLocked(ctx, "memory_origin_events")
	}
	if s.tableExistsLocked(ctx, "memory_jobs") {
		st.Jobs, _ = s.countLocked(ctx, "memory_jobs")
	}
	if s.tableExistsLocked(ctx, "memory_dependencies") {
		st.Dependencies, _ = s.countLocked(ctx, "memory_dependencies")
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

// ApplyMigration upgrades a v3 database to v4. It is the `picoclaw memory
// migrate` entry point and never deletes entries.
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

	// `memory migrate` is an explicit maintenance command: it always leaves a
	// VACUUM INTO snapshot, even when the schema was already current, so an
	// operator has a rollback point for the data repairs in the same command.
	if s.metaValueLocked(ctx, "last_migration_backup") == "" {
		backup, err := s.backupWithRotation(ctx, "migrate")
		if err != nil {
			return MigrationReport{}, err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO meta(key,value) VALUES('last_migration_backup',?)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, backup); err != nil {
			return MigrationReport{}, err
		}
	}

	report := MigrationReport{PreviousSchemaVersion: previous}
	s.pendingMigration = false
	report.SchemaVersion = s.schemaVersionLocked(ctx)
	report.Backup = s.metaValueLocked(ctx, "last_migration_backup")
	report.FTSTokenizer = s.metaValueLocked(ctx, "fts_tokenizer")
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&report.Total)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE status='active'`).Scan(&report.Active)
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`).Scan(&report.EmptyPredicate)
	if report.FTSTokenizer != "none" {
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&report.FTSRows)
	}
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE domain_id <> 'legacy_unassigned'`).Scan(&report.EntriesWithDomain)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sources`).Scan(&report.SourcesRebuilt)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_domains`).Scan(&report.DomainsCreated)
	return report, nil
}

// RepairPreviewData reports how many rows a v4 self-heal would touch. It does
// not write anything. The v3 hard-coded data repairs are gone with the v3
// migration chain.
func (s *Store) RepairPreviewData(ctx context.Context) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	queries := map[string]string{
		"empty_predicate":      `SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`,
		"uncategorized_active": `SELECT COUNT(*) FROM entries WHERE status='active' AND predicate='待分类'`,
		"unsourced":            `SELECT COUNT(*) FROM entries e WHERE NOT EXISTS (SELECT 1 FROM sources s WHERE s.entry_id=e.id)`,
		"legacy_unassigned":    `SELECT COUNT(*) FROM entries WHERE domain_id='legacy_unassigned'`,
		"stale_schema":         `SELECT COUNT(*) FROM meta WHERE key='schema_version' AND value <> '4'`,
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

// RepairData re-runs the idempotent v4 segment: it heals missing columns,
// tables, indexes and the FTS projection without deleting data.
func (s *Store) RepairData(ctx context.Context) (MigrationReport, error) {
	previous := s.schemaVersionLocked(ctx)
	report := MigrationReport{PreviousSchemaVersion: previous}
	if err := s.migrateSchema(ctx); err != nil {
		return MigrationReport{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	backup, err := s.backupWithRotation(ctx, "repair")
	if err != nil {
		return MigrationReport{}, err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta(key,value) VALUES('last_migration_backup',?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, backup); err != nil {
		return MigrationReport{}, err
	}
	report.SchemaVersion = s.schemaVersionLocked(ctx)
	s.pendingMigration = false
	report.Backup = backup
	report.FTSTokenizer = s.metaValueLocked(ctx, "fts_tokenizer")
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&report.Total)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE status='active'`).Scan(&report.Active)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sources`).Scan(&report.SourcesRebuilt)
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
