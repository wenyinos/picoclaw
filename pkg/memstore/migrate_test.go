package memstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func createV1DB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta(key,value) VALUES('revision','7'),('schema_version','1')`,
		`CREATE TABLE entries (
			id TEXT PRIMARY KEY, realm TEXT NOT NULL, layer TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'user', audience TEXT NOT NULL DEFAULT 'all',
			subject TEXT NOT NULL DEFAULT '', predicate TEXT NOT NULL DEFAULT '',
			value TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, normalized TEXT NOT NULL,
			content_hash TEXT NOT NULL, simhash INTEGER NOT NULL DEFAULT 0,
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
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE personas (
			id TEXT PRIMARY KEY, name TEXT NOT NULL, version INTEGER NOT NULL,
			content TEXT NOT NULL, active INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`INSERT INTO entries
			(id,realm,layer,scope,audience,subject,predicate,value,text,normalized,content_hash,
			 simhash,template_key,cluster_id,occurrences,importance,emotional_weight,valence,
			 confidence,status,event_at,expires_at,created_at,updated_at,last_used_at,use_count,attributes)
		 VALUES('e1','fictional','relationship','user','private','姐弟故事','','旧值','关系文本',
			'关系文本','hash-v1-1',0,'','',1,0.9,0,0,0.8,'active',NULL,NULL,1,1,NULL,0,'{}')`,
		`INSERT INTO relationship_state(key,value,valid_from,valid_to,confidence,updated_at)
		 VALUES('ai_name','宝贝',1,NULL,0.9,1)`,
		`INSERT INTO personas(id,name,version,content,active,created_at)
		 VALUES('default','Pico',1,'冷静',1,1)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("v1 schema: %v\n%s", err, stmt)
		}
	}
}

func TestSchemaV2Migration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	createV1DB(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	status, err := s.MigrationStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema = %d, want %d", status.SchemaVersion, CurrentSchemaVersion)
	}
	if status.EmptyPredicate != 1 || status.Relationship != 1 || status.Personas != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}

	if !s.hasColumnLocked(ctx, "entries", "facet") {
		t.Fatal("entries.facet missing")
	}
	if !s.hasColumnLocked(ctx, "relationship_state", "expires_at") {
		t.Fatal("relationship_state.expires_at missing")
	}
	if !s.personasHasCompositeKeyLocked(ctx) {
		t.Fatal("personas still has old primary key")
	}

	if _, err := s.UpsertPersonaScoped(ctx, "default", "Pico", "冷静 v2", true, RealmReal, "private", "user"); err != nil {
		t.Fatalf("second persona version: %v", err)
	}
	if _, err := s.UpsertPersonaScoped(ctx, "default", "Pico", "冷静 v3", true, RealmReal, "private", "user"); err != nil {
		t.Fatalf("third persona version: %v", err)
	}
	personas, err := s.ListPersonas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(personas) != 3 {
		t.Fatalf("personas = %d, want 3", len(personas))
	}

	report, err := s.ApplyV2Migration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.PreviousSchemaVersion != 1 {
		t.Fatalf("previous schema = %d, want 1", report.PreviousSchemaVersion)
	}
	if report.UpdatedPredicates != 1 {
		t.Fatalf("updated predicates = %d, want 1", report.UpdatedPredicates)
	}
	if _, err := os.Stat(report.Backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	var predicate string
	if err := s.db.QueryRowContext(ctx, `SELECT predicate FROM entries WHERE id='e1'`).Scan(&predicate); err != nil {
		t.Fatal(err)
	}
	if predicate != "待分类" {
		t.Fatalf("predicate = %q, want 待分类", predicate)
	}

	if err := s.SetRelationship(ctx, "stage", "第一版", 0.9); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRelationship(ctx, "stage", "第二版", 0.9); err != nil {
		t.Fatal(err)
	}
	history, err := s.GetRelationshipHistory(ctx, "stage")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Value != "第一版" {
		t.Fatalf("history = %+v, want one old value", history)
	}

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
