package memstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// CurrentSchemaVersion is the schema version expected by this build.
const CurrentSchemaVersion = 2

// MigrationStatus describes whether the database needs the v2 migration.
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
	Total                  int    `json:"total"`
	Active                 int    `json:"active"`
	DBPath                 string `json:"db_path"`
	DBSize                 int64  `json:"db_size"`
	Backup                 string `json:"backup,omitempty"`
	LastMigrationAt        int64  `json:"last_migration_at,omitempty"`
}

// MigrationReport is returned after applying the v2 migration.
type MigrationReport struct {
	PreviousSchemaVersion int    `json:"previous_schema_version"`
	SchemaVersion         int    `json:"schema_version"`
	Total                 int    `json:"total"`
	Active                int    `json:"active"`
	EmptyPredicate        int    `json:"empty_predicate"`
	UpdatedPredicates     int    `json:"updated_predicates"`
	MappedRelationships   int    `json:"mapped_relationships"`
	Backup                string `json:"backup"`
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

func (s *Store) backupLocked(ctx context.Context, label string) (string, error) {
	base := s.path + ".backup-" + label + "-" + time.Now().Format("20060102-150405")
	path := base
	for i := 1; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = fmt.Sprintf("%s-%d", base, i)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return "", fmt.Errorf("backup before migration: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return path, nil
}

// migrateSchema upgrades an existing v1 database to the v2 schema. It is
// idempotent and safe to call on every Open.
func (s *Store) migrateSchema(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.migrateSchemaLocked(ctx)
	return err
}

func (s *Store) migrateSchemaLocked(ctx context.Context) (int, string, error) {
	previous := s.schemaVersionLocked(ctx)
	hasFacet := s.hasColumnLocked(ctx, "entries", "facet")
	hasExpires := s.hasColumnLocked(ctx, "relationship_state", "expires_at")
	hasHistory := s.tableExistsLocked(ctx, "relationship_state_history")
	personasExists := s.tableExistsLocked(ctx, "personas")
	personasV2 := s.hasColumnLocked(ctx, "personas", "realm") &&
		s.hasColumnLocked(ctx, "personas", "audience") &&
		s.hasColumnLocked(ctx, "personas", "scope") &&
		s.personasHasCompositeKeyLocked(ctx)

	if previous >= CurrentSchemaVersion && hasFacet && hasExpires && hasHistory && personasV2 {
		return previous, "", nil
	}

	backup := ""
	var err error
	hasExistingData := false
	if s.tableExistsLocked(ctx, "entries") {
		var n int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&n) == nil && n > 0 {
			hasExistingData = true
		}
	}
	if previous > 0 || hasExistingData {
		backup, err = s.backupLocked(ctx, "schema-v2")
		if err != nil {
			return previous, "", err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return previous, backup, err
	}
	defer func() { _ = tx.Rollback() }()

	if !hasFacet {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE entries ADD COLUMN facet TEXT NOT NULL DEFAULT ''`); err != nil {
			return previous, backup, err
		}
	}
	if !hasExpires {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE relationship_state ADD COLUMN expires_at INTEGER`); err != nil {
			return previous, backup, err
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
		return previous, backup, err
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS ix_entries_facet ON entries(realm, subject, predicate, facet, status)`); err != nil {
		return previous, backup, err
	}

	if !personasV2 {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS personas_v2`); err != nil {
			return previous, backup, err
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
			return previous, backup, err
		}
		if personasExists {
			if _, err := tx.ExecContext(ctx, `INSERT INTO personas_v2
				(id,name,version,content,active,realm,audience,scope,created_at)
				SELECT id,name,version,content,active,'real','private','user',created_at FROM personas`); err != nil {
				return previous, backup, err
			}
			if _, err := tx.ExecContext(ctx, `DROP TABLE personas`); err != nil {
				return previous, backup, err
			}
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE personas_v2 RENAME TO personas`); err != nil {
			return previous, backup, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS ix_personas_active ON personas(active, realm, audience)`); err != nil {
		return previous, backup, err
	}

	now := time.Now().UnixMilli()
	if err := s.setMetaLocked(ctx, tx, "schema_version", fmt.Sprintf("%d", CurrentSchemaVersion)); err != nil {
		return previous, backup, err
	}
	if backup != "" {
		if err := s.setMetaLocked(ctx, tx, "last_migration_backup", backup); err != nil {
			return previous, backup, err
		}
		if err := s.setMetaLocked(ctx, tx, "last_migration_at", fmt.Sprintf("%d", now)); err != nil {
			return previous, backup, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='revision'`); err != nil {
		return previous, backup, err
	}
	if err := tx.Commit(); err != nil {
		return previous, backup, err
	}
	s.revision.Add(1)
	return previous, backup, nil
}

// MigrationStatus reports whether the database still needs schema or data
// migration. It is safe to call while the service is running.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	version := s.schemaVersionLocked(ctx)
	hasFacet := s.hasColumnLocked(ctx, "entries", "facet")
	hasExpires := s.hasColumnLocked(ctx, "relationship_state", "expires_at")
	hasHistory := s.tableExistsLocked(ctx, "relationship_state_history")
	personasV2 := s.hasColumnLocked(ctx, "personas", "realm") &&
		s.hasColumnLocked(ctx, "personas", "audience") &&
		s.hasColumnLocked(ctx, "personas", "scope") &&
		s.personasHasCompositeKeyLocked(ctx)

	st := MigrationStatus{
		SchemaVersion:        version,
		CurrentVersion:       CurrentSchemaVersion,
		NeedsSchemaMigration: version < CurrentSchemaVersion || !hasFacet || !hasExpires || !hasHistory || !personasV2,
		DBPath:               s.path,
		Backup:               s.metaValueLocked(ctx, "last_migration_backup"),
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
	if info, err := os.Stat(s.path); err == nil {
		st.DBSize = info.Size()
	}
	st.NeedsDataMigration = st.EmptyPredicate > 0
	st.NeedsMigration = st.NeedsSchemaMigration || st.NeedsDataMigration
	return st, nil
}

// ApplyV2Migration backs up the database, upgrades the schema, and turns
// empty predicates into the explicit "待分类" value. It never deletes entries
// and never changes status.
func (s *Store) ApplyV2Migration(ctx context.Context) (MigrationReport, error) {
	previous := s.openedSchema
	if previous == 0 {
		previous = s.schemaVersionLocked(ctx)
	}
	if err := s.migrateSchema(ctx); err != nil {
		return MigrationReport{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	backup, err := s.backupLocked(ctx, "v2")
	if err != nil {
		return MigrationReport{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MigrationReport{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var empty int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`).Scan(&empty); err != nil {
		return MigrationReport{}, err
	}
	now := time.Now().UnixMilli()
	mapped := 0
	for id, mapping := range relationshipFacetMappings {
		res, err := tx.ExecContext(ctx,
			`UPDATE entries SET predicate=?, facet=?, updated_at=?
			 WHERE id=? AND status='active'
			   AND (TRIM(predicate)='' OR predicate='待分类' OR TRIM(facet)='')`,
			mapping.predicate, mapping.facet, now, id)
		if err != nil {
			return MigrationReport{}, err
		}
		n, _ := res.RowsAffected()
		mapped += int(n)
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE entries SET predicate='待分类', updated_at=? WHERE TRIM(predicate)=''`, now)
	if err != nil {
		return MigrationReport{}, err
	}
	updated, _ := res.RowsAffected()
	updated += int64(mapped)
	if err := s.setMetaLocked(ctx, tx, "schema_version", fmt.Sprintf("%d", CurrentSchemaVersion)); err != nil {
		return MigrationReport{}, err
	}
	if err := s.setMetaLocked(ctx, tx, "last_migration_backup", backup); err != nil {
		return MigrationReport{}, err
	}
	if err := s.setMetaLocked(ctx, tx, "last_migration_at", fmt.Sprintf("%d", now)); err != nil {
		return MigrationReport{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='revision'`); err != nil {
		return MigrationReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return MigrationReport{}, err
	}
	s.revision.Add(1)

	report := MigrationReport{
		PreviousSchemaVersion: previous,
		SchemaVersion:         CurrentSchemaVersion,
		EmptyPredicate:        empty,
		UpdatedPredicates:     int(updated),
		MappedRelationships:   mapped,
		Backup:                backup,
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&report.Total)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE status='active'`).Scan(&report.Active)
	return report, nil
}
