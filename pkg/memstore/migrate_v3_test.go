package memstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func createV2DB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta(key,value) VALUES('revision','9'),('schema_version','2')`,
		`CREATE TABLE entries (
			id TEXT PRIMARY KEY, realm TEXT NOT NULL, layer TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'user', audience TEXT NOT NULL DEFAULT 'all',
			subject TEXT NOT NULL DEFAULT '', predicate TEXT NOT NULL DEFAULT '',
			facet TEXT NOT NULL DEFAULT '', value TEXT NOT NULL DEFAULT '', text TEXT NOT NULL,
			normalized TEXT NOT NULL, content_hash TEXT NOT NULL, simhash INTEGER NOT NULL DEFAULT 0,
			template_key TEXT NOT NULL DEFAULT '', cluster_id TEXT NOT NULL DEFAULT '',
			occurrences INTEGER NOT NULL DEFAULT 1, importance REAL NOT NULL DEFAULT 0.5,
			emotional_weight REAL NOT NULL DEFAULT 0, valence REAL NOT NULL DEFAULT 0,
			confidence REAL NOT NULL DEFAULT 0.5, status TEXT NOT NULL DEFAULT 'active',
			event_at INTEGER, expires_at INTEGER, created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL, last_used_at INTEGER,
			use_count INTEGER NOT NULL DEFAULT 0, attributes TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE sources (
			entry_id TEXT NOT NULL, source_type TEXT NOT NULL,
			source_id TEXT NOT NULL DEFAULT '', session_key TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			PRIMARY KEY (entry_id, source_type, source_id, session_key)
		)`,
		`CREATE TABLE supersedes (
			entry_id TEXT NOT NULL, superseded_id TEXT NOT NULL,
			created_at INTEGER NOT NULL, PRIMARY KEY (entry_id, superseded_id)
		)`,
		`CREATE TABLE relationship_state (
			key TEXT PRIMARY KEY, value TEXT NOT NULL, valid_from INTEGER NOT NULL,
			valid_to INTEGER, confidence REAL NOT NULL DEFAULT 0.8,
			updated_at INTEGER NOT NULL, expires_at INTEGER
		)`,
		`CREATE TABLE relationship_state_history (
			key TEXT NOT NULL, value TEXT NOT NULL, valid_from INTEGER NOT NULL,
			valid_to INTEGER NOT NULL, confidence REAL NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY (key, valid_from)
		)`,
		`CREATE TABLE personas (
			id TEXT NOT NULL, name TEXT NOT NULL, version INTEGER NOT NULL,
			content TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 0,
			realm TEXT NOT NULL DEFAULT 'real', audience TEXT NOT NULL DEFAULT 'private',
			scope TEXT NOT NULL DEFAULT 'user', created_at INTEGER NOT NULL,
			PRIMARY KEY (id, version)
		)`,
		`INSERT INTO entries
			(id,realm,layer,scope,audience,subject,predicate,facet,value,text,normalized,content_hash,
			 simhash,template_key,cluster_id,occurrences,importance,emotional_weight,valence,
			 confidence,status,event_at,expires_at,created_at,updated_at,last_used_at,use_count,attributes)
		 VALUES('e1','real','profile','user','all','user','','','','旧事实','旧事实','hash-v2-1',0,'','',1,0.9,0,0,0.8,'active',NULL,NULL,1,1,NULL,0,'{}'),
		       ('e2','real','profile','user','all','user','','','','第二条','第二条','hash-v2-2',0,'','',1,0.8,0,0,0.8,'active',NULL,NULL,1,1,NULL,0,'{}')`,
		`INSERT INTO sources(entry_id,source_type,source_id,session_key,created_at)
		 VALUES('e1','user_statement','s1','',1)`,
		`INSERT INTO sources(entry_id,source_type,source_id,session_key,created_at)
		 VALUES('ghost','user_statement','s1','',1)`,
		`INSERT INTO supersedes(entry_id,superseded_id,created_at) VALUES('e1','missing',1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("v2 schema: %v\n%s", err, stmt)
		}
	}
}

func TestSchemaV3FromV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	createV2DB(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if got := s.schemaVersionLocked(ctx); got != 3 {
		t.Fatalf("schema = %d, want 3", got)
	}
	if !s.hasColumnLocked(ctx, "entries", "pinned_core") ||
		!s.hasColumnLocked(ctx, "entries", "valid_to") ||
		!s.hasColumnLocked(ctx, "sources", "confidence") {
		t.Fatal("v3 columns missing")
	}
	if !s.tableExistsLocked(ctx, "memory_operations") || !s.tableExistsLocked(ctx, "memory_relations") {
		t.Fatal("v3 tables missing")
	}

	// Orphan accounting is written to meta and the orphan rows are gone.
	if got := s.metaValueLocked(ctx, "v3_dropped_orphan_sources"); got != "1" {
		t.Fatalf("orphan sources = %q, want 1", got)
	}
	if got := s.metaValueLocked(ctx, "v3_dropped_orphan_supersedes"); got != "1" {
		t.Fatalf("orphan supersedes = %q, want 1", got)
	}
	var orphanSources int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sources s WHERE NOT EXISTS (SELECT 1 FROM entries e WHERE e.id=s.entry_id)`).
		Scan(&orphanSources)
	if orphanSources != 0 {
		t.Fatalf("orphan sources left: %d", orphanSources)
	}

	// FTS is aligned with entries.
	fts, err := s.FTSCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var total int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&total)
	if fts != total {
		t.Fatalf("fts rows = %d, entries = %d", fts, total)
	}

	// Running the migration twice is a no-op.
	before, _ := s.FTSCount(ctx)
	if err := s.migrateSchema(ctx); err != nil {
		t.Fatalf("second migrateSchema: %v", err)
	}
	after, _ := s.FTSCount(ctx)
	if before != after {
		t.Fatalf("second migration changed FTS rows: %d -> %d", before, after)
	}

	// Data repair fills the empty predicates and backfills evidence.
	report, err := s.ApplyMigration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.EmptyPredicate != 2 || report.UpdatedPredicates != 2 {
		t.Fatalf("unexpected repair report: %+v", report)
	}
	var empty int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE TRIM(predicate)=''`).Scan(&empty)
	if empty != 0 {
		t.Fatalf("empty predicates left: %d", empty)
	}
	if report.Backup == "" {
		t.Fatal("repair did not create a backup")
	}
}

func TestSchemaV3SelfHealsMissingObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{Text: "自愈测试", Layer: LayerProfile}); err != nil {
		t.Fatal(err)
	}
	// Drop one v3 object; the next Open must recreate it instead of failing.
	if _, err := s.db.ExecContext(ctx, `DROP TABLE memory_operations`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after dropping v3 object: %v", err)
	}
	defer s2.Close()
	if !s2.tableExistsLocked(context.Background(), "memory_operations") {
		t.Fatal("memory_operations was not recreated")
	}
}

func TestSchemaV1ToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	createV1DB(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	if got := s.schemaVersionLocked(ctx); got != 3 {
		t.Fatalf("schema = %d, want 3", got)
	}
	for _, check := range []struct{ table, column string }{
		{"entries", "facet"},
		{"entries", "pinned_core"},
		{"relationship_state", "expires_at"},
		{"sources", "evidence_text"},
	} {
		if !s.hasColumnLocked(ctx, check.table, check.column) {
			t.Fatalf("%s.%s missing", check.table, check.column)
		}
	}
	if !s.personasHasCompositeKeyLocked(ctx) {
		t.Fatal("personas composite key missing")
	}
	// The v1 row survives the two-step upgrade.
	var text string
	if err := s.db.QueryRowContext(ctx, `SELECT text FROM entries WHERE id='e1'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "关系文本" {
		t.Fatalf("v1 row lost: %q", text)
	}
}
