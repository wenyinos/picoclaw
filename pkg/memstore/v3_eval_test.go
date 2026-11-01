package memstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type evalCases struct {
	Anchors []struct {
		Query string `json:"query"`
		Facet string `json:"facet"`
	} `json:"anchors"`
	Firewall []string `json:"firewall"`
	Temporal []struct {
		Text      string `json:"text"`
		Precision string `json:"precision"`
		Event     bool   `json:"event"`
	} `json:"temporal"`
}

// TestV3EvalCases is the deterministic 60-case gate. It never touches a real
// workspace: the fixture is built inside the test.
//
// Categories:
//  1. anchors            12  Top-1 = 100%
//  2. write firewall     10  real/profile pollution = 0
//  3. time and narrative 12  precision = 100%
//  4. relation recall    10  Recall@5 = 100%
//  5. correction history  8  active-only + inspect trail = 100%
//  6. forget/channel      8  leakage = 0
func TestV3EvalCases(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "v3_eval_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases evalCases
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Anchors) != 12 || len(cases.Firewall) != 10 || len(cases.Temporal) != 12 {
		t.Fatalf("fixture size = %d/%d/%d, want 12/10/12",
			len(cases.Anchors), len(cases.Firewall), len(cases.Temporal))
	}

	t.Run("anchors", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		if _, err := s.Add(ctx, AddInput{
			Text:  "设计说明：本质定调、角色卡、边界授权、尺度与氛围、创作方向都会被提及。",
			Layer: LayerProcedural, Importance: 0.95,
		}); err != nil {
			t.Fatal(err)
		}
		for _, c := range cases.Anchors {
			if _, err := s.Add(ctx, AddInput{
				Text: "T0 正文 " + c.Facet, Layer: LayerProcedural, Realm: RealmFictional,
				Audience: "private", Predicate: "写作规则", Facet: c.Facet,
				Importance: 0.8, PinnedCore: true,
			}); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range cases.Anchors {
			entries, err := s.SearchEntries(ctx, SearchOptions{Query: c.Query, Limit: 5})
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) == 0 || entries[0].Facet != c.Facet {
				t.Errorf("anchor %q: top-1 = %+v", c.Query, entries)
			}
		}
	})

	t.Run("write_firewall", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		for _, sample := range cases.Firewall {
			results, err := s.ExtractTurnAt(ctx, sample, "", "s1", time.Now(), true)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 0 {
				t.Errorf("dialogue produced memories: %q -> %+v", sample, results)
			}
		}
		polluted, err := s.List(ctx, SearchOptions{Realm: RealmReal, Layer: LayerProfile, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(polluted) != 0 {
			t.Fatalf("real/profile pollution = %d", len(polluted))
		}
	})

	t.Run("time_and_narrative", func(t *testing.T) {
		at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local)
		for _, c := range cases.Temporal {
			hint := ParseRelativeTime(c.Text, at, time.Local)
			if hint == nil {
				t.Errorf("%q did not parse", c.Text)
				continue
			}
			if hint.Precision != c.Precision {
				t.Errorf("%q precision = %q, want %q", c.Text, hint.Precision, c.Precision)
			}
			if c.Event && hint.EventAt == nil {
				t.Errorf("%q missing event_at", c.Text)
			}
			if !c.Event && hint.ValidFrom == nil {
				t.Errorf("%q missing valid_from", c.Text)
			}
		}
		// Narrative phrases are intentionally not parsed.
		for _, phrase := range []string{"第一次见面", "第二次", "那天"} {
			if hint := ParseRelativeTime(phrase, at, time.Local); hint != nil {
				t.Errorf("%q should be narrative, got %+v", phrase, hint)
			}
		}
	})

	t.Run("relation_recall", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		scenes := []string{"海边的日落", "雨中的车站", "深夜的厨房", "清晨的街道", "雪后的屋顶"}
		details := []string{"第一次提起", "后来补充的细节"}
		var seeds []Entry
		for i := 0; i < 5; i++ {
			seed, err := s.Add(ctx, AddInput{
				Text: "共同经历：" + scenes[i], Layer: LayerEpisodic,
				Audience: "private", Importance: 0.7,
			})
			if err != nil {
				t.Fatal(err)
			}
			seeds = append(seeds, *seed.Entry)
			for j := 0; j < 2; j++ {
				target, err := s.Add(ctx, AddInput{
					Text:  "关联记忆：" + scenes[i] + "·" + details[j],
					Layer: LayerEpisodic, Audience: "private", Importance: 0.6,
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.AddRelation(ctx, Relation{
					SourceID: seed.Entry.ID, TargetID: target.Entry.ID, RelationType: RelRelatedTo,
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		related, err := s.ExpandOneHop(ctx, seeds, 3, SearchOptions{
			Channel: "pico", PrivateChannels: []string{"pico"}, IncludeFic: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(related) != 10 {
			t.Fatalf("one-hop recall = %d, want 10", len(related))
		}
	})

	t.Run("correction_history", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		old, err := s.Add(ctx, AddInput{
			Text: "旧值 A", Layer: LayerProfile, Subject: "user",
			Predicate: "nickname", Facet: "称呼", Value: "A",
		})
		if err != nil {
			t.Fatal(err)
		}
		replacement, err := s.Correct(ctx, old.Entry.ID, AddInput{Text: "新值 B", Value: "B"})
		if err != nil {
			t.Fatal(err)
		}
		active, err := s.SearchEntries(ctx, SearchOptions{Query: "值", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range active {
			if e.ID == old.Entry.ID {
				t.Fatal("superseded entry returned by default search")
			}
		}
		inspect, err := s.Inspect(ctx, old.Entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if inspect.Entry.Status != StatusSuperseded || len(inspect.Supersedes) == 0 {
			t.Fatalf("inspect lost the version trail: %+v", inspect)
		}
		if replacement.Entry == nil || replacement.Entry.Status != StatusActive {
			t.Fatalf("replacement not active: %+v", replacement)
		}
	})

	t.Run("forget_and_channel", func(t *testing.T) {
		s := newTestStore(t)
		ctx := context.Background()
		for i := 0; i < 4; i++ {
			text := "需要遗忘的记忆 " + string(rune('A'+i))
			added, err := s.Add(ctx, AddInput{Text: text, Layer: LayerProfile, Audience: "private"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Forget(ctx, added.Entry.ID, false); err != nil {
				t.Fatal(err)
			}
			res, err := s.Add(ctx, AddInput{Text: text, Layer: LayerProfile, Audience: "private"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Action != "blocked_forget" {
				t.Fatalf("forget leakage for %q: %s", text, res.Action)
			}
		}
		fictional, err := s.Add(ctx, AddInput{
			Text: "虚构亲密记忆", Layer: LayerProcedural, Realm: RealmFictional,
			Audience: "private", PinnedCore: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		now := nowMillis()
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO memory_clusters(id,realm,title,topic,status,created_at,updated_at)
			 VALUES('capsule:eval','fictional','评测胶囊','t','active',?,?)`, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO memory_cluster_members(cluster_id,entry_id,ordinal) VALUES('capsule:eval',?,1)`,
			fictional.Entry.ID); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			channel string
			inject  bool
		}{
			{"discord", false},
			{"discord", true},
			{"pico", false},
			{"pico", true},
		} {
			capsule, err := s.LoadCapsule(ctx, "capsule:eval", tc.channel, []string{"pico"}, tc.inject, 1200)
			if err != nil {
				t.Fatal(err)
			}
			wantLeak := tc.channel == "pico" && tc.inject
			if wantLeak != (len(capsule.Items) > 0) {
				t.Fatalf("capsule channel=%s inject=%v leaked=%v", tc.channel, tc.inject, len(capsule.Items) > 0)
			}
		}
	})
}
