package memstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// createV3DB builds a minimal but complete v3 database: every table that the
// removed v3 segment created, with v3 (not v4) columns. The v4 migration must
// upgrade it in place without losing evidence fields.
func createV3DB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta(key,value) VALUES('revision','7'),('schema_version','3'),('fts_tokenizer','none')`,
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
			use_count INTEGER NOT NULL DEFAULT 0, attributes TEXT NOT NULL DEFAULT '{}',
			event_time_precision TEXT NOT NULL DEFAULT 'unknown',
			event_timezone TEXT NOT NULL DEFAULT '',
			event_time_source TEXT NOT NULL DEFAULT '',
			valid_from INTEGER, valid_to INTEGER,
			pinned_core INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX ux_entries_hash ON entries(content_hash) WHERE status='active'`,
		`CREATE TABLE sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entry_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL DEFAULT '',
			session_key TEXT NOT NULL DEFAULT '',
			message_id INTEGER,
			source_id TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT 'explicit',
			evidence_text TEXT NOT NULL DEFAULT '',
			extraction_model TEXT NOT NULL DEFAULT '',
			prompt_version TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 1.0,
			user_confirmed INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			UNIQUE(entry_id, source_type, source_id, session_key)
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
		`CREATE TABLE memory_relations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, source_id TEXT NOT NULL,
			target_id TEXT NOT NULL, relation_type TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 1.0, source_type TEXT NOT NULL DEFAULT 'inferred',
			created_at INTEGER NOT NULL, UNIQUE(source_id, target_id, relation_type)
		)`,
		`CREATE TABLE memory_clusters (
			id TEXT PRIMARY KEY, realm TEXT NOT NULL DEFAULT 'real', title TEXT NOT NULL,
			topic TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active',
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE memory_cluster_members (
			cluster_id TEXT NOT NULL, entry_id TEXT NOT NULL,
			ordinal INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (cluster_id, entry_id)
		)`,
		`CREATE TABLE memory_summary_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT, cluster_id TEXT NOT NULL, version INTEGER NOT NULL,
			summary TEXT NOT NULL, confidence REAL NOT NULL DEFAULT 0.8,
			model TEXT NOT NULL DEFAULT '', prompt_version TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active', created_at INTEGER NOT NULL, superseded_at INTEGER,
			UNIQUE(cluster_id, version)
		)`,
		`CREATE TABLE memory_operations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, operation TEXT NOT NULL,
			target_entry_id TEXT NOT NULL DEFAULT '',
			replacement_entry_id TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '', affected_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO entries
			(id,realm,layer,scope,audience,subject,predicate,facet,value,text,normalized,content_hash,
			 simhash,template_key,cluster_id,occurrences,importance,emotional_weight,valence,
			 confidence,status,event_at,expires_at,created_at,updated_at,last_used_at,use_count,attributes)
		 VALUES('e1','fictional','relationship','user','private','姐弟故事','身份关系','公开身份','姐姐',
			'她是姐姐','她是姐姐','hash-v3-1',0,'','',2,0.9,0,0,0.8,'active',NULL,NULL,1,1,NULL,0,'{}')`,
		`INSERT INTO sources
			(entry_id,conversation_id,session_key,message_id,source_id,source_type,evidence_text,
			 extraction_model,prompt_version,confidence,user_confirmed,created_at)
		 VALUES('e1','conv-1','sess-1',42,'msg-42','explicit','她是姐姐，今年三十岁',
			'rule-v3','p1',0.93,1,111)`,
		`INSERT INTO sources
			(entry_id,conversation_id,session_key,message_id,source_id,source_type,evidence_text,
			 extraction_model,prompt_version,confidence,user_confirmed,created_at)
		 VALUES('e1','conv-1','sess-1',43,'msg-43','explicit','姐姐这周来看我',
			'rule-v3','p1',0.88,0,222)`,
		`INSERT INTO relationship_state(key,value,valid_from,valid_to,confidence,updated_at,expires_at)
		 VALUES('ai_name','宝贝',1,NULL,0.9,1,NULL)`,
		`INSERT INTO relationship_state_history(key,value,valid_from,valid_to,confidence,updated_at)
		 VALUES('ai_name','亲爱的',1,2,0.9,2)`,
		`INSERT INTO personas(id,name,version,content,active,realm,audience,scope,created_at)
		 VALUES('default','Pico',1,'冷静',1,'real','private','user',1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("v3 schema: %v\n%s", err, stmt)
		}
	}
}

func TestSchemaV4FromV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	createV3DB(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	// Opening a v3 database must not migrate it: the memory service fails
	// closed until the owner runs the explicit migration.
	if !s.PendingMigration() {
		t.Fatal("v3 database must open in fail-closed mode")
	}
	if _, err := s.Get(ctx, "e1"); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("read before migration = %v, want ErrMigrationRequired", err)
	}
	if _, err := s.Add(ctx, AddInput{Text: "写入应被拒绝"}); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("write before migration = %v, want ErrMigrationRequired", err)
	}

	report, err := s.ApplyMigration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.PreviousSchemaVersion != 3 || report.SchemaVersion != 4 {
		t.Fatalf("migration report = %+v", report)
	}
	if s.PendingMigration() {
		t.Fatal("store still pending after explicit migration")
	}

	status, err := s.MigrationStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema = %d, want %d", status.SchemaVersion, CurrentSchemaVersion)
	}
	if status.NeedsSchemaMigration {
		t.Fatalf("schema migration still pending: %+v", status)
	}

	// v4 columns on entries.
	for _, col := range v4EntryColumns {
		if !s.hasColumnLocked(ctx, "entries", col) {
			t.Fatalf("entries.%s missing after v4 migration", col)
		}
	}
	// v4 tables.
	for _, table := range v4Tables {
		if !s.tableExistsLocked(ctx, table) {
			t.Fatalf("table %s missing after v4 migration", table)
		}
	}
	// Default domain exists and existing entries are isolated, not guessed.
	d, err := s.GetDomain(ctx, DomainLegacyUnassigned)
	if err != nil {
		t.Fatalf("legacy domain: %v", err)
	}
	if d.Status != "active" {
		t.Fatalf("legacy domain status = %q", d.Status)
	}

	e, err := s.Get(ctx, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if e.DomainID != DomainLegacyUnassigned {
		t.Fatalf("domain = %q, want %q", e.DomainID, DomainLegacyUnassigned)
	}
	if e.RowRevision != 1 {
		t.Fatalf("row_revision = %d, want 1", e.RowRevision)
	}
	if len(e.Sources) != 2 {
		t.Fatalf("sources = %d, want 2 (evidence must survive the rebuild)", len(e.Sources))
	}
	for _, src := range e.Sources {
		if src.EvidenceText == "" {
			t.Fatal("evidence_text lost during sources rebuild")
		}
		if src.ExtractionModel != "rule-v3" || src.PromptVersion != "p1" {
			t.Fatalf("extraction metadata lost: %+v", src)
		}
		if src.Confidence != 0.93 && src.Confidence != 0.88 {
			t.Fatalf("confidence changed: %v", src.Confidence)
		}
		if src.OriginEventID != "" || src.OriginRevision != 1 {
			t.Fatalf("legacy source must not fake an origin event: %+v", src)
		}
	}
	var confirmed, messages int
	for _, src := range e.Sources {
		if src.UserConfirmed {
			confirmed++
		}
		if src.MessageID != nil {
			messages++
		}
	}
	if confirmed != 1 || messages != 2 {
		t.Fatalf("confirmation/message flags lost: confirmed=%d messages=%d", confirmed, messages)
	}

	// Relationship state survives and is scoped to the legacy domain.
	rel, err := s.GetRelationship(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rel) != 1 || rel[0].Value != "宝贝" || rel[0].DomainID != DomainLegacyUnassigned {
		t.Fatalf("relationship state = %+v", rel)
	}

	// Second open is a no-op and remains healthy.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	st2, err := s2.MigrationStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st2.SchemaVersion != CurrentSchemaVersion || st2.NeedsSchemaMigration {
		t.Fatalf("reopen status = %+v", st2)
	}
}

func TestSchemaRejectsPreV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta(key,value) VALUES('revision','1'),('schema_version','2')`,
		`CREATE TABLE entries (id TEXT PRIMARY KEY, realm TEXT NOT NULL, layer TEXT NOT NULL,
			text TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("expected v2 database to be rejected, got nil error")
	}
}

func TestSchemaRejectsNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta(key,value) VALUES('revision','1'),('schema_version','9')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("expected future schema to be rejected")
	}
}

func TestSchemaV4SelfHeal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	createV3DB(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if !s.PendingMigration() {
		t.Fatal("v3 database must open in fail-closed mode")
	}
	if _, err := s.ApplyMigration(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// A missing unique index is derived state and can be healed without
	// touching entry/source data.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX ux_entries_hash`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.PendingMigration() {
		t.Fatal("a v4 database missing objects must open in fail-closed mode")
	}
	if _, err := s2.Get(ctx, "e1"); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("read on incomplete schema = %v, want ErrMigrationRequired", err)
	}
	if _, err := s2.ApplyMigration(ctx); err != nil {
		t.Fatal(err)
	}
	if !s2.objectExistsLocked(ctx, "index", "ux_entries_hash") {
		t.Fatal("ux_entries_hash not healed")
	}
	e, err := s2.Get(ctx, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Sources) != 2 {
		t.Fatalf("sources after self-heal = %d, want 2", len(e.Sources))
	}
	status, err := s2.MigrationStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsSchemaMigration {
		t.Fatalf("self-heal left status dirty: %+v", status)
	}
}

func TestV4MissingObligationTableRequiresRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE memory_jobs`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.PendingMigration() {
		t.Fatal("missing memory_jobs must open fail-closed")
	}
	if _, err := s2.ApplyMigration(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "non_repairable") {
		t.Fatalf("repair error = %v, want non_repairable", err)
	}
	if s2.tableExistsLocked(context.Background(), "memory_jobs") {
		t.Fatal("empty memory_jobs must not be fabricated")
	}
}

func TestFreshBootstrapIsAtomicAndStartsAtV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if s.openedSchema != CurrentSchemaVersion || s.PendingMigration() {
		t.Fatalf("fresh schema=%d pending=%v", s.openedSchema, s.PendingMigration())
	}
	var version string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != "4" {
		t.Fatalf("schema_version=%q", version)
	}
	if !s.ftsCompleteLocked(ctx, s.detectFTS(ctx)) {
		t.Fatal("fresh bootstrap left FTS incomplete")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRepairPreviewData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	preview, err := s.RepairPreviewData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := preview["legacy_unassigned"]; !ok {
		t.Fatalf("preview missing legacy_unassigned: %+v", preview)
	}
	report, err := s.RepairData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("repair schema = %d", report.SchemaVersion)
	}
	backupDir := filepath.Join(filepath.Dir(path), "backups")
	if _, err := os.Stat(backupDir); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestApplyMigrationReportsBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	createV3DB(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.PendingMigration() {
		t.Fatal("v3 database must open in fail-closed mode")
	}
	report, err := s.ApplyMigration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.PreviousSchemaVersion != 3 || report.SchemaVersion != 4 {
		t.Fatalf("report = %+v", report)
	}
	if report.Total != 1 || report.Active != 1 {
		t.Fatalf("report counts = %+v", report)
	}
	if report.Backup == "" {
		t.Fatal("v3→v4 migration must record a backup path")
	}
	if _, err := os.Stat(report.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestRelationshipTTLStillFiltersExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	past := time.Now().Add(-time.Minute).UnixMilli()
	if err := s.SetRelationshipWithTTL(ctx, "mood", "过期", 0.9, &past); err != nil {
		t.Fatal(err)
	}
	rel, err := s.GetRelationship(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rel {
		if r.Key == "mood" {
			t.Fatalf("expired relationship state leaked: %+v", r)
		}
	}
}
