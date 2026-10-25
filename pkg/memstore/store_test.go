package memstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAdd_DedupeAndConflict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	first, err := s.Add(ctx, AddInput{Text: "用户对花生过敏，推荐食物时避开花生", Layer: LayerProfile, Importance: 0.95})
	if err != nil || first.Action != "added" {
		t.Fatalf("first add = %+v, %v", first, err)
	}
	dup, err := s.Add(ctx, AddInput{Text: "用户对花生过敏，推荐食物时避开花生", Layer: LayerProfile})
	if err != nil || dup.Action != "duplicate" {
		t.Fatalf("duplicate = %+v, %v", dup, err)
	}
	near, err := s.Add(ctx, AddInput{Text: "用户对花生过敏，推荐食物时请避开", Layer: LayerProfile})
	if err != nil || (near.Action != "merged" && near.Action != "duplicate") {
		t.Fatalf("near duplicate = %+v, %v", near, err)
	}

	old, err := s.Add(ctx, AddInput{Text: "用户希望被称为小明", Subject: "user", Predicate: "nickname", Value: "小明"})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.Add(ctx, AddInput{Text: "用户希望被称为阿明", Subject: "user", Predicate: "nickname", Value: "阿明"})
	if err != nil || newer.Action != "superseded" {
		t.Fatalf("conflict = %+v, %v", newer, err)
	}
	gotOld, _ := s.Get(ctx, old.Entry.ID)
	if gotOld.Status != StatusSuperseded {
		t.Fatalf("old status = %q, want superseded", gotOld.Status)
	}
}

func TestContextAndAudience(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.UpsertPersona(ctx, "default", "Pico", "冷静、简洁", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRelationship(ctx, "ai_name", "小P", 0.9); err != nil {
		t.Fatal(err)
	}
	for _, in := range []AddInput{
		{Text: "用户喜欢喝乌龙茶", Layer: LayerProfile, Importance: 0.9},
		{Text: "不要在工作群提到亲密称呼", Layer: LayerBoundary, Importance: 0.9, Audience: "private"},
		{Text: "2026年9月一起去看了海", Layer: LayerEpisodic, Realm: RealmReal, Importance: 0.7, EmotionalWeight: 0.8, Audience: "private"},
	} {
		if _, err := s.Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}

	fixed := s.BuildFixedContext(ctx, 800, false, "pico", []string{"pico"})
	for _, want := range []string{"角色设定", "关系状态", "小P", "乌龙茶", "不要在工作群"} {
		if !strings.Contains(fixed, want) {
			t.Fatalf("fixed context missing %q: %s", want, fixed)
		}
	}

	// Private memories must not leak into a channel-less retrieval.
	public := s.BuildRetrievalContext(ctx, "海", "", 400, false, []string{"pico"})
	if strings.Contains(public, "一起去看了海") {
		t.Fatalf("private memory leaked without channel: %s", public)
	}
	private := s.BuildRetrievalContext(ctx, "海", "pico", 400, false, []string{"pico"})
	if !strings.Contains(private, "一起去看了海") {
		t.Fatalf("private memory missing for private channel: %s", private)
	}
}

func TestImportLegacy(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "state", "evolution"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{
		{"id": "1", "summary": "请分析这段对话，判断是否值得长期记住的用户个人信息", "success": true},
		{"id": "2", "summary": "请分析这段对话，判断是否值得长期记住的用户个人信息", "success": true},
		{"id": "3", "summary": "hello", "success": true},
		{"id": "4", "summary": "配置 systemd 自启动服务", "success": true},
		{"id": "5", "summary": "失败的部署任务", "success": false},
	}
	f, _ := os.Create(filepath.Join(dir, "state", "evolution", "task-records.jsonl"))
	for _, r := range rows {
		b, _ := json.Marshal(r)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()
	meta, _ := json.Marshal(map[string]any{"key": "s1", "summary": "一段角色扮演剧情摘要"})
	_ = os.WriteFile(filepath.Join(dir, "sessions", "s1.meta.json"), meta, 0o644)

	var report strings.Builder
	r, err := s.ImportLegacy(context.Background(), dir, &report)
	if err != nil {
		t.Fatal(err)
	}
	if r.TaskRows != 5 || r.TemplateFolded != 2 || r.ChitchatSkipped != 1 || r.FailuresArchived != 1 {
		t.Fatalf("unexpected report: %+v\n%s", r, report.String())
	}
	if r.SummariesImported != 1 {
		t.Fatalf("summaries imported = %d, want 1", r.SummariesImported)
	}
	pending, _ := s.List(context.Background(), SearchOptions{Status: StatusPending, Limit: 10})
	if len(pending) != 1 {
		t.Fatalf("pending summaries = %d, want 1", len(pending))
	}
}

func TestExtractTurn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ExtractTurn(ctx, "以后叫我小明", "好的", "s1", true); err != nil {
		t.Fatal(err)
	}
	rel, _ := s.GetRelationship(ctx)
	foundName := false
	for _, r := range rel {
		if r.Key == "user_name" && r.Value == "小明" {
			foundName = true
		}
	}
	if !foundName {
		t.Fatalf("user_name not extracted: %+v", rel)
	}

	if _, err := s.ExtractTurn(ctx, "记住我对花生过敏", "好的", "s1", true); err != nil {
		t.Fatal(err)
	}
	profile, _ := s.List(ctx, SearchOptions{Layer: LayerProfile, Status: StatusActive, Limit: 10})
	if len(profile) == 0 {
		t.Fatal("explicit memory not extracted")
	}

	if _, err := s.ExtractTurn(ctx, "2026年10月30日去东京出差一周", "", "s1", true); err != nil {
		t.Fatal(err)
	}
	events, _ := s.List(ctx, SearchOptions{Layer: LayerEpisodic, Status: StatusActive, Limit: 10})
	if len(events) != 1 || events[0].ExpiresAt == nil {
		t.Fatalf("dated event not extracted with expiry: %+v", events)
	}

	if res, err := s.ExtractTurn(ctx, "hello", "hi", "s1", true); err != nil || len(res) != 0 {
		t.Fatalf("chitchat should not extract: %+v %v", res, err)
	}

	for _, tc := range []struct{ user, assistant string }{
		{"根据你的设定，简单介绍一下你自己。", "好的，我是 PicoClaw。"},
		{"今天天气不错，我们随便聊聊吧。", "是啊，秋高气爽。"},
		{"我们来扮演一个故事", "好的。"},
	} {
		res, err := s.ExtractTurn(ctx, tc.user, tc.assistant, "s1", true)
		if err != nil || len(res) != 0 {
			t.Fatalf("ordinary/roleplay chat should not extract: %q -> %+v, %v", tc.user, res, err)
		}
	}
	pending, _ := s.List(ctx, SearchOptions{Realm: RealmFictional, Status: StatusPending, Limit: 10})
	if len(pending) != 0 {
		t.Fatalf("roleplay keywords should not create pending memories: %+v", pending)
	}
}
