package memstore

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSyntheticRetrievalQualityMatrix is a small, content-safe quality gate:
// positive queries must retrieve the expected memory, forbidden queries must
// not leak private/expired/cross-chat rows, and retrieval must respect budget.
func TestSyntheticRetrievalQualityMatrix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	expired := time.Now().Add(-time.Hour).UnixMilli()

	oolong, err := s.Add(ctx, AddInput{
		Text: "用户喜欢乌龙茶", Layer: LayerProfile, Audience: "private", Importance: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	deploy, err := s.Add(ctx, AddInput{
		Text: "部署脚本使用蓝色海豚流程", Layer: LayerProcedural, Audience: "all", Importance: 0.8,
	})
	if err != nil {
		t.Fatal(err)
	}
	fictional, err := s.Add(ctx, AddInput{
		Text: "角色卡夜莺的说话风格", Layer: LayerProcedural, Realm: RealmFictional,
		Audience: "all", Importance: 0.7,
	})
	if err != nil {
		t.Fatal(err)
	}
	expiredEntry, err := s.Add(ctx, AddInput{
		Text: "过期的临时偏好", Layer: LayerProfile, Audience: "private", ExpiresAt: &expired,
	})
	if err != nil {
		t.Fatal(err)
	}

	type testCase struct {
		name       string
		query      string
		chatKind   string
		includeFic bool
		wantID     string
		forbidIDs  []string
	}
	for _, tc := range []testCase{
		{
			name: "private positive", query: "乌龙茶", chatKind: ChatKindPrivate,
			wantID: oolong.Entry.ID,
		},
		{
			name: "procedural positive", query: "蓝色海豚", chatKind: ChatKindPrivate,
			wantID: deploy.Entry.ID,
		},
		{
			name: "group hides private", query: "乌龙茶", chatKind: ChatKindGroup,
			forbidIDs: []string{oolong.Entry.ID},
		},
		{
			name: "fictional filter", query: "夜莺", chatKind: ChatKindPrivate,
			includeFic: true, wantID: fictional.Entry.ID,
		},
		{
			name: "expired hidden", query: "过期的临时偏好", chatKind: ChatKindPrivate,
			forbidIDs: []string{expiredEntry.Entry.ID},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := s.Search(ctx, SearchOptions{
				Query: tc.query, Channel: "pico", ChatKindHint: tc.chatKind,
				AllLayers: true, IncludeFic: tc.includeFic,
				DomainID: DomainLegacyUnassigned, ExcludeExpired: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, got := range results {
				if got.ID == tc.wantID {
					found = true
				}
				for _, forbidden := range tc.forbidIDs {
					if got.ID == forbidden {
						t.Fatalf("forbidden entry %s returned for query %q", forbidden, tc.query)
					}
				}
			}
			if tc.wantID != "" && !found {
				t.Fatalf("expected entry %s not retrieved for %q", tc.wantID, tc.query)
			}
		})
	}

	text := s.BuildRetrievalContextFor(
		ctx, "蓝色海豚", "pico", ChatKindPrivate, 40, false, nil,
	)
	if text != "" && EstimateTokens(text) > 40 {
		t.Fatalf("retrieval budget exceeded: %d > 40: %q", EstimateTokens(text), text)
	}
	if got := s.BuildRetrievalContextFor(
		ctx, "zzzqqqxxx不存在的乱码关键词", "pico", ChatKindPrivate, 1200, false, nil,
	); strings.TrimSpace(got) != "" {
		t.Fatalf("garbage query returned context: %q", got)
	}
}
