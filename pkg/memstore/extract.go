package memstore

import (
	"context"
	"regexp"
	"strings"
	"time"
)

var (
	datePattern    = regexp.MustCompile(`(20\d{2})[年\-/](\d{1,2})[月\-/](\d{1,2})日?`)
	weekPattern    = regexp.MustCompile(`(一周|一个星期|7天|七天)`)
	dayPattern     = regexp.MustCompile(`(一天|1天|当天)`)
	nicknamePat    = regexp.MustCompile(`(?:以后|今后)?(?:请)?叫我\s*[「"']?([^，。,.;；\s]{1,12})`)
	aiNicknamePat  = regexp.MustCompile(`(?:以后|今后)?(?:我)?叫你\s*[「"']?([^，。,.;；\s]{1,12})`)
	preferencePat  = regexp.MustCompile(`(?:我|本人)(喜欢|不喜欢|偏好|讨厌|爱吃|不吃|对[^，。]{1,12}过敏)`)
	roleplayMarker = []string{"扮演", "角色扮演", "剧情", "设定", "人设", "世界观", "虚构", "故事线"}
)

// ExtractTurn applies conservative rule-based extraction to one completed turn.
// It only creates memories for explicit user statements, so ordinary chat does
// not pollute the store.
func (s *Store) ExtractTurn(
	ctx context.Context,
	userText, assistantText, sessionKey string,
	reviewRequired bool,
) ([]AddResult, error) {
	text := strings.TrimSpace(userText)
	if text == "" || s == nil {
		return nil, nil
	}
	now := time.Now()
	results := []AddResult{}
	add := func(in AddInput) {
		in.SourceType = "user_statement"
		in.SessionKey = sessionKey
		in.Audience = "private"
		in.Confidence = 0.8
		if in.Realm == "" {
			in.Realm = RealmReal
		}
		if in.Layer == "" {
			in.Layer = LayerProfile
		}
		if in.Importance == 0 {
			in.Importance = 0.7
		}
		if in.Realm == RealmUnknown && reviewRequired {
			in.ForceStatus = StatusPending
		}
		if r, err := s.Add(ctx, in); err == nil && r != nil {
			results = append(results, *r)
		}
	}

	// Explicit "remember this".
	if strings.Contains(text, "记住") || strings.Contains(text, "记一下") || strings.Contains(text, "别忘了") {
		add(AddInput{Text: text, Layer: LayerProfile, Importance: 0.95})
		return results, nil
	}

	// Preferred names.
	if m := nicknamePat.FindStringSubmatch(text); len(m) == 2 {
		name := strings.TrimSpace(m[1])
		if name != "" {
			add(AddInput{Text: "用户希望被称为「" + name + "」", Subject: "user", Predicate: "nickname", Value: name, Importance: 0.95})
			_ = s.SetRelationship(ctx, "user_name", name, 0.9)
		}
	}
	if m := aiNicknamePat.FindStringSubmatch(text); len(m) == 2 {
		name := strings.TrimSpace(m[1])
		if name != "" {
			add(AddInput{Text: "用户希望 AI 被称为「" + name + "」", Subject: "ai", Predicate: "nickname", Value: name, Importance: 0.95})
			_ = s.SetRelationship(ctx, "ai_name", name, 0.9)
		}
	}

	// Preferences and allergies.
	if preferencePat.MatchString(text) {
		add(AddInput{Text: text, Layer: LayerProfile, Importance: 0.9})
	}

	// Birthdays / anniversaries.
	if strings.Contains(text, "生日是") || strings.Contains(text, "纪念日是") {
		key := "birthday"
		if strings.Contains(text, "纪念日") {
			key = "anniversary"
		}
		add(AddInput{Text: text, Layer: LayerRelationship, Importance: 0.9})
		_ = s.SetRelationship(ctx, key, text, 0.9)
	}

	// Dated events with a short duration.
	if m := datePattern.FindStringSubmatch(text); len(m) == 4 {
		if !strings.Contains(text, "出差") && !strings.Contains(text, "旅行") && !strings.Contains(text, "会议") &&
			!strings.Contains(text, "考试") && !strings.Contains(text, "生日") {
			return results, nil
		}
		year, month, day := atoi(m[1]), atoi(m[2]), atoi(m[3])
		eventAt := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
		var expires *int64
		switch {
		case weekPattern.MatchString(text):
			v := eventAt.AddDate(0, 0, 7).UnixMilli()
			expires = &v
		case dayPattern.MatchString(text):
			v := eventAt.AddDate(0, 0, 1).UnixMilli()
			expires = &v
		}
		ev := eventAt.UnixMilli()
		add(AddInput{
			Text: text, Layer: LayerEpisodic, Importance: 0.6,
			EventAt: &ev, ExpiresAt: expires, EmotionalWeight: 0.3,
		})
		return results, nil
	}

	// Role-play context: keep it out of the real profile.
	if containsAny(text+" "+assistantText, roleplayMarker) {
		add(AddInput{
			Text: text, Realm: RealmFictional, Layer: LayerEpisodic,
			Importance: 0.5, Audience: "private",
			ForceStatus: func() string {
				if reviewRequired {
					return StatusPending
				}
				return StatusActive
			}(),
		})
	}

	_ = now
	return results, nil
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
