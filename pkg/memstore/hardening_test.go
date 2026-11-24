package memstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportEntriesPagesPastRequestLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 520; i++ {
		if _, err := s.Add(ctx, AddInput{
			Text:  fmt.Sprintf("导出台账 %064x", int64(i)*2654435761),
			Layer: LayerProcedural,
			Realm: RealmWork,
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List(ctx, SearchOptions{Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 100 {
		t.Fatalf("List returned %d, want the capped 100", len(list))
	}
	exported, err := s.ExportEntries(ctx, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if len(exported) != total {
		t.Fatalf("ExportEntries returned %d of %d entries", len(exported), total)
	}
	if len(exported) <= len(list) {
		t.Fatalf("export was still capped: exported=%d list=%d", len(exported), len(list))
	}
}

func TestStoreRejectsSecretsAtEveryWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Add(ctx, AddInput{Text: "我的密码是 abc123", Layer: LayerProfile}); err == nil {
		t.Fatal("text secret must be rejected by Store.Add")
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "普通文本", Layer: LayerProfile, Sensitivity: SensitivitySecret,
	}); err == nil {
		t.Fatal("explicit secret sensitivity must be rejected by Store.Add")
	}

	normal, err := s.Add(ctx, AddInput{Text: "普通设定", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(ctx, normal.Entry.ID, AddInput{Text: "我的私钥是 xyz"}); err == nil {
		t.Fatal("secret correction must be rejected")
	}

	results, err := s.ExtractTurnAt(ctx, "记住我的密码是 abc123", "", "sess-secret", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("extraction stored a secret: %+v", results)
	}
}

func TestCorrectInheritsDomainSensitivityAndPinned(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	added, err := s.Add(ctx, AddInput{
		Text: "核心设定", Layer: LayerProfile, DomainID: "domain:a",
		Sensitivity: SensitivitySensitive, PinnedCore: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	corrected, err := s.Correct(ctx, added.Entry.ID, AddInput{
		Text: "核心设定二", Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if corrected.Entry.DomainID != "domain:a" {
		t.Fatalf("domain = %q, want domain:a", corrected.Entry.DomainID)
	}
	if corrected.Entry.Sensitivity != SensitivitySensitive {
		t.Fatalf("sensitivity = %q, want sensitive", corrected.Entry.Sensitivity)
	}
	if !corrected.Entry.PinnedCore {
		t.Fatal("corrected entry lost pinned_core")
	}
	old, err := s.Get(ctx, added.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != StatusSuperseded {
		t.Fatalf("old status = %q, want superseded", old.Status)
	}
}

func TestMissingLedgerFailsClosedForForget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(ctx, AddInput{Text: "需要遗忘的记忆", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(LedgerPathFor(path)); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reads must stay available without a ledger: %v", err)
	}
	defer s2.Close()
	if s2.ForgetLedgerRef() != nil {
		t.Fatal("missing ledger must not be silently created")
	}
	health, err := s2.LedgerHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if available, _ := health["available"].(bool); available {
		t.Fatalf("ledger reported available: %+v", health)
	}
	if _, err := s2.Get(ctx, added.Entry.ID); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if _, err := s2.Forget(ctx, added.Entry.ID, false); err == nil {
		t.Fatal("destructive forget must fail closed without a ledger")
	}
}

func TestCorruptLedgerIdentityIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(ctx, AddInput{Text: "需要遗忘的记忆", Layer: LayerProfile})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ledgerPath := LedgerPathFor(path)
	db, err := sql.Open("sqlite", ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE ledger_meta SET value='broken' WHERE key='hmac_key'`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reads must stay available with a damaged ledger: %v", err)
	}
	defer s2.Close()
	if s2.ForgetLedgerRef() != nil {
		t.Fatal("damaged ledger identity must not be replaced")
	}
	if _, err := s2.Forget(ctx, added.Entry.ID, false); err == nil {
		t.Fatal("destructive forget must fail closed with a damaged ledger")
	}
}

func TestBackupIncludesForgetLedger(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{Text: "备份测试", Layer: LayerProfile}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backupWithRotation(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(filepath.Dir(s.Path()), "backups", "forget-ledger.db.bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("ledger backups = %d, want 1", len(backups))
	}
	ledger, err := OpenExistingForgetLedger(backups[0])
	if err != nil {
		t.Fatalf("backup ledger is not usable: %v", err)
	}
	defer ledger.Close()
	if ledger.UUID() == "" || ledger.KeyID() == "" {
		t.Fatalf("backup ledger lost its identity: uuid=%q keyID=%q", ledger.UUID(), ledger.KeyID())
	}
	verify, err := s.VerifyLedgerBackup(backups[0])
	if err != nil {
		t.Fatalf("backup verification failed: %v", err)
	}
	if match, _ := verify["match"].(bool); !match {
		t.Fatalf("backup identity mismatch: %+v", verify)
	}
}

func TestGroupChatFixedAndRetrievalBlocksHidePrivate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, in := range []AddInput{
		{Text: "PRIVATE_PROFILE", Layer: LayerProfile, Audience: "private"},
		{Text: "PUBLIC_PROFILE", Layer: LayerProfile, Audience: "all"},
		{Text: "PRIVATE_BOUNDARY", Layer: LayerBoundary, Audience: "private"},
		{Text: "PUBLIC_BOUNDARY", Layer: LayerBoundary, Audience: "all"},
		{Text: "PRIVATE_RETRIEVAL", Layer: LayerEpisodic, Audience: "private"},
		{Text: "PUBLIC_RETRIEVAL", Layer: LayerEpisodic, Audience: "all"},
	} {
		if _, err := s.Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetRelationshipFull(ctx, RelationshipUpdate{
		Key: "stage", Value: "PRIVATE_STAGE", Audience: "private",
	}); err != nil {
		t.Fatal(err)
	}

	fixed, _ := s.BuildFixedContextFor(ctx, 2000, false, "pico", ChatKindGroup, nil)
	for _, marker := range []string{"PRIVATE_PROFILE", "PRIVATE_BOUNDARY", "PRIVATE_STAGE"} {
		if strings.Contains(fixed, marker) {
			t.Fatalf("group fixed context leaked %s: %s", marker, fixed)
		}
	}
	for _, marker := range []string{"PUBLIC_PROFILE", "PUBLIC_BOUNDARY"} {
		if !strings.Contains(fixed, marker) {
			t.Fatalf("group fixed context missing %s: %s", marker, fixed)
		}
	}

	retrieval := s.BuildRetrievalContextFor(ctx, "RETRIEVAL", "pico", ChatKindGroup, 2000, false, nil)
	if strings.Contains(retrieval, "PRIVATE_RETRIEVAL") {
		t.Fatalf("group retrieval leaked private memory: %s", retrieval)
	}
	if !strings.Contains(retrieval, "PUBLIC_RETRIEVAL") {
		t.Fatalf("group retrieval missing public memory: %s", retrieval)
	}
}

func TestFTSPermissionFilterPrecedesLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 160; i++ {
		if _, err := s.Add(ctx, AddInput{
			Text:       fmt.Sprintf("边界授权 %064x", int64(i)*2654435761),
			Layer:      LayerProcedural,
			Audience:   "private",
			Importance: 0.9,
		}); err != nil {
			t.Fatal(err)
		}
	}
	public, err := s.Add(ctx, AddInput{
		Text: "边界授权 PUBLIC_SENTINEL", Layer: LayerProcedural,
		Audience: "all", Importance: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var privateCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entries WHERE audience='private'`).Scan(&privateCount); err != nil {
		t.Fatal(err)
	}
	if privateCount <= 100 {
		t.Fatalf("fixture did not produce >100 private candidates: %d", privateCount)
	}

	results, err := s.Search(ctx, SearchOptions{
		Query: "边界授权", ChatKindHint: ChatKindGroup, Channel: "pico",
		AllLayers: true, Limit: 20, DomainID: DomainLegacyUnassigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range results {
		if e.Audience == "private" {
			t.Fatalf("group search returned private memory: %+v", e)
		}
		if e.ID == public.Entry.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("public match was crowded out by invisible candidates (%d results)", len(results))
	}
}

func TestSummaryGenerationMembershipAndSource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first, err := s.Add(ctx, AddInput{Text: "一起去看海", Layer: LayerEpisodic})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Add(ctx, AddInput{Text: "不在簇里的记忆", Layer: LayerEpisodic})
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := s.CreateCluster(ctx, Cluster{Title: "阶段", Realm: RealmReal})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddClusterMembers(ctx, cluster.ID, []string{first.Entry.ID}); err != nil {
		t.Fatal(err)
	}
	var generation int
	if err := s.db.QueryRowContext(ctx,
		`SELECT generation FROM memory_clusters WHERE id=?`, cluster.ID).Scan(&generation); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PublishSummaryAtGeneration(ctx, cluster.ID, "旧代", []SummaryClaim{
		{ClaimID: "c1", Text: "看海", EntryIDs: []string{first.Entry.ID}},
	}, "test", "v1", 99); err == nil {
		t.Fatal("stale generation must be rejected")
	}
	if _, err := s.PublishSummaryAtGeneration(ctx, cluster.ID, "跨簇", []SummaryClaim{
		{ClaimID: "c1", Text: "跨簇", EntryIDs: []string{second.Entry.ID}},
	}, "test", "v1", generation); err == nil {
		t.Fatal("evidence outside the cluster must be rejected")
	}

	version, err := s.PublishSummaryAtGeneration(ctx, cluster.ID, "看海", []SummaryClaim{
		{ClaimID: "c1", Text: "看海", EntryIDs: []string{first.Entry.ID}},
	}, "test", "v1", generation)
	if err != nil {
		t.Fatal(err)
	}
	var sourceID int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT source_id FROM memory_summary_evidence WHERE summary_version_id=?`,
		version.ID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if sourceID <= 0 {
		t.Fatalf("summary evidence source_id = %d, want a real source", sourceID)
	}
}

func TestCorrectInvalidatesDependentSummary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	entry, err := s.Add(ctx, AddInput{Text: "一起去看了海", Layer: LayerEpisodic})
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := s.CreateCluster(ctx, Cluster{Title: "阶段", Realm: RealmReal})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddClusterMembers(ctx, cluster.ID, []string{entry.Entry.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GenerateSummary(ctx, cluster.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(ctx, entry.Entry.ID, AddInput{Text: "一起看了湖"}); err != nil {
		t.Fatal(err)
	}
	versions, err := s.ListSummaries(ctx, cluster.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range versions {
		if v.Status == "active" {
			t.Fatalf("summary stayed active after correction: %+v", versions)
		}
	}
}
