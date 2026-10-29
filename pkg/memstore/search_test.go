package memstore

import (
	"context"
	"os"
	"testing"
)

func TestSearchEntries_AllLayersAndFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	add := func(text, realm, layer, audience string) {
		t.Helper()
		if _, err := s.Add(ctx, AddInput{
			Text: text, Realm: realm, Layer: layer, Audience: audience, Importance: 0.8,
		}); err != nil {
			t.Fatalf("Add(%q): %v", text, err)
		}
	}
	add("我的生日是1989年6月4日", RealmReal, LayerProfile, "private")
	add("蓝色海豚 第七号仓库 搬箱子", RealmFictional, LayerProcedural, "all")
	add("token 的不同含义有哪些", RealmWork, LayerProcedural, "all")
	add("用户希望被称为阿泽", RealmFictional, LayerRelationship, "private")

	tests := []struct {
		name  string
		opts  SearchOptions
		want  int
		check func(Entry) bool
	}{
		{
			name: "profile birthday is searchable",
			opts: SearchOptions{Query: "生日", Limit: 20},
			want: 1,
			check: func(e Entry) bool {
				return e.Realm == RealmReal && e.Layer == LayerProfile && e.Audience == "private"
			},
		},
		{
			name: "fictional filter is honored",
			opts: SearchOptions{Query: "蓝色海豚", Realm: RealmFictional, Limit: 20},
			want: 1,
			check: func(e Entry) bool {
				return e.Realm == RealmFictional && e.Layer == LayerProcedural
			},
		},
		{
			name: "wrong realm returns nothing",
			opts: SearchOptions{Query: "蓝色海豚", Realm: RealmWork, Limit: 20},
			want: 0,
		},
		{
			name: "garbage query returns nothing",
			opts: SearchOptions{Query: "zzzqqqxxx不存在的乱码关键词", Limit: 20},
			want: 0,
		},
		{
			name: "private memories are visible to admin search",
			opts: SearchOptions{Query: "阿泽", Limit: 20},
			want: 1,
			check: func(e Entry) bool {
				return e.Audience == "private" && e.Layer == LayerRelationship
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.SearchEntries(ctx, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.want {
				t.Fatalf("results = %d, want %d: %+v", len(got), tt.want, got)
			}
			if tt.check != nil && !tt.check(got[0]) {
				t.Fatalf("unexpected result: %+v", got[0])
			}
		})
	}
}

func TestSearch_PromptRetrievalFiltersIrrelevantAndPrivate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, text := range []string{
		"token 的不同含义有哪些",
		"刚才的项目配置",
		"gta online容易和队友断开连接如何处理",
	} {
		if _, err := s.Add(ctx, AddInput{
			Text: text, Realm: RealmWork, Layer: LayerProcedural, Audience: "all",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "蓝色海豚 第七号仓库 搬箱子", Realm: RealmFictional, Layer: LayerProcedural, Audience: "all",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "我的生日是1989年6月4日", Realm: RealmReal, Layer: LayerEpisodic,
		Audience: "private", Importance: 0.9,
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("unrelated query does not return the three work records", func(t *testing.T) {
		got, err := s.Search(ctx, SearchOptions{Query: "zzzqqqxxx", Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("unexpected unrelated results: %+v", got)
		}
	})

	t.Run("fictional excluded unless enabled", func(t *testing.T) {
		got, err := s.Search(ctx, SearchOptions{Query: "蓝色海豚", Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("fictional should be excluded by default: %+v", got)
		}

		got, err = s.Search(ctx, SearchOptions{Query: "蓝色海豚", IncludeFic: true, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Realm != RealmFictional {
			t.Fatalf("fictional should be returned when enabled: %+v", got)
		}
	})

	t.Run("private only enters configured private channel", func(t *testing.T) {
		got, err := s.Search(ctx, SearchOptions{Query: "生日", Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("private memory should not leak into an unfiltered prompt: %+v", got)
		}

		got, err = s.Search(ctx, SearchOptions{
			Query: "生日", Channel: "pico", PrivateChannels: []string{"pico"}, Limit: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Audience != "private" {
			t.Fatalf("private memory should be available in private channel: %+v", got)
		}
	})
}

func TestRepairReclassifyWithBackup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, AddInput{
		Text: "姐姐的角色设定：蓝色海豚", Realm: RealmWork, Layer: LayerProcedural, Audience: "all",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "token 的不同含义", Realm: RealmWork, Layer: LayerProcedural, Audience: "all",
	}); err != nil {
		t.Fatal(err)
	}

	filter := RepairFilter{Realm: RealmWork, Layer: LayerProcedural, Query: "蓝色海豚"}
	preview, err := s.PreviewRepair(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Count != 1 || len(preview.Entries) != 1 {
		t.Fatalf("preview = %+v, want 1 entry", preview)
	}

	target := RepairTarget{Realm: RealmFictional, Layer: LayerProcedural, Audience: "private"}
	updated, backup, err := s.ApplyRepair(ctx, filter, target)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}

	got, err := s.SearchEntries(ctx, SearchOptions{Query: "蓝色海豚", Realm: RealmFictional, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Audience != "private" {
		t.Fatalf("repair result = %+v", got)
	}

	if _, _, err := s.ApplyRepair(ctx, RepairFilter{}, target); err == nil {
		t.Fatal("empty repair filter should be rejected")
	}
}
