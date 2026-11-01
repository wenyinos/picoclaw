package memstore

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	datePattern   = regexp.MustCompile(`(20\d{2})[年\-/](\d{1,2})[月\-/](\d{1,2})日?`)
	weekPattern   = regexp.MustCompile(`(一周|一个星期|7天|七天)`)
	dayPattern    = regexp.MustCompile(`(一天|1天|当天)`)
	nicknamePat   = regexp.MustCompile(`(?:以后|今后)?(?:请)?叫我\s*[「"']?([^，。,.;；\s]{1,12})`)
	aiNicknamePat = regexp.MustCompile(`(?:以后|今后)?(?:我)?叫你\s*[「"']?([^，。,.;；\s]{1,12})`)
	preferencePat = regexp.MustCompile(`(?:我|本人)(喜欢|不喜欢|偏好|讨厌|爱吃|不吃|对[^，。]{1,12}过敏)`)

	// imperativeRemember is anchored at the start of the sentence. The old
	// substring check fired on mid-sentence dialogue ("记住，我所有的反抗…"),
	// which is how a line of role-play became a real profile fact.
	imperativeRemember = regexp.MustCompile(`^(?:请|帮我|麻烦|记得|你)?\s*(?:记住|记一下|记下|别忘了)`)

	quoteSpanPattern = regexp.MustCompile(`["“”「」『』][^"“”「」『』]{2,}["“”「」『』]`)
	titleSpanPattern = regexp.MustCompile(`【[^】]{2,}】`)

	moodPattern = regexp.MustCompile(`^(?:今天)?(?:好累|累了|抱抱|想你了|困了|难过|开心)[。！!？?~～\s]*$`)
)

// isRoleplayLike reports whether a message looks like dialogue or a setting
// document rather than a user instruction. It requires both a quote/title span
// and a meaningful share of the text, so a long genuine preference that
// happens to contain quotes is not discarded.
func isRoleplayLike(text string) bool {
	if imperativeRemember.MatchString(text) {
		return false
	}
	total := utf8.RuneCountInString(text)
	if total == 0 {
		return false
	}
	if titleSpanPattern.MatchString(text) && total > 120 {
		return true
	}
	if m := quoteSpanPattern.FindString(text); m != "" {
		if float64(utf8.RuneCountInString(m))/float64(total) > 0.35 {
			return true
		}
	}
	return false
}

// ExtractTurn applies conservative rule-based extraction to one completed turn
// using the current time as the evidence anchor.
func (s *Store) ExtractTurn(
	ctx context.Context,
	userText, assistantText, sessionKey string,
	reviewRequired bool,
) ([]AddResult, error) {
	return s.ExtractTurnAt(ctx, userText, assistantText, sessionKey, time.Now(), reviewRequired)
}

// ExtractTurnAt applies conservative rule-based extraction to one completed
// turn. It only creates memories for explicit user statements, so ordinary chat
// does not pollute the store. Assistant replies are not inspected: words such
// as "设定" or "剧情" appear in normal assistant prose and must not create
// memories by themselves.
//
// Branch order is fixed: mood → role-play guard → imperative remember →
// names → preferences → birthdays → dated events. Reordering changes results
// (for example, running names first would capture "叫我" inside dialogue).
func (s *Store) ExtractTurnAt(
	ctx context.Context,
	userText, assistantText, sessionKey string,
	turnAt time.Time,
	reviewRequired bool,
) ([]AddResult, error) {
	text := strings.TrimSpace(userText)
	if text == "" || s == nil {
		return nil, nil
	}
	if turnAt.IsZero() {
		turnAt = time.Now()
	}
	results := []AddResult{}
	add := func(in AddInput) {
		if in.SourceType == "" {
			in.SourceType = SourceInferred
		}
		in.SessionKey = sessionKey
		in.Audience = "private"
		if in.Confidence == 0 {
			in.Confidence = 0.8
		}
		if in.EvidenceText == "" {
			in.EvidenceText = text
		}
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

	// 1. Emotional one-liners only update relationship state with a 24h TTL.
	if moodPattern.MatchString(text) {
		expires := turnAt.Add(24 * time.Hour).UnixMilli()
		_ = s.SetRelationshipWithTTL(ctx, "current_mood", text, 0.9, &expires)
		return results, nil
	}

	// 2. Role-play and setting documents never enter real/profile.
	if isRoleplayLike(text) {
		return results, nil
	}

	// 3. Explicit "remember this".
	if imperativeRemember.MatchString(text) {
		add(AddInput{
			Text: text, Layer: LayerProfile, Predicate: "用户画像", Facet: "明确要求记住",
			Importance: 0.9, Confidence: 0.75, SourceType: SourceInferred, EvidenceText: text,
		})
		return results, nil
	}

	// 4. Preferred names. Dialogue such as 「你叫我什么？」 must not be read as
	// an instruction, so matches inside quotes or interrogatives are skipped.
	if idx := nicknamePat.FindStringSubmatchIndex(text); len(idx) == 4 {
		name := strings.TrimSpace(text[idx[2]:idx[3]])
		if validNickname(name) && !insideQuote(text, idx[0]) {
			add(AddInput{Text: "用户希望被称为「" + name + "」", Subject: "user", Predicate: "nickname",
				Facet: "称呼", Value: name, Importance: 0.95, SourceType: SourceExplicit})
			_ = s.SetRelationship(ctx, "user_name", name, 0.9)
		}
	}
	if idx := aiNicknamePat.FindStringSubmatchIndex(text); len(idx) == 4 {
		name := strings.TrimSpace(text[idx[2]:idx[3]])
		if validNickname(name) && !insideQuote(text, idx[0]) {
			add(AddInput{Text: "用户希望 AI 被称为「" + name + "」", Subject: "ai", Predicate: "nickname",
				Facet: "称呼", Value: name, Importance: 0.95, SourceType: SourceExplicit})
			_ = s.SetRelationship(ctx, "ai_name", name, 0.9)
		}
	}

	// 5. Preferences and allergies.
	if preferencePat.MatchString(text) {
		add(AddInput{Text: text, Layer: LayerProfile, Predicate: "偏好", Facet: "日常偏好",
			Importance: 0.9, SourceType: SourceExplicit})
	}

	// 6. Birthdays / anniversaries.
	if strings.Contains(text, "生日是") || strings.Contains(text, "纪念日是") {
		key := "birthday"
		if strings.Contains(text, "纪念日") {
			key = "anniversary"
		}
		add(AddInput{Text: text, Layer: LayerRelationship, Predicate: key, Facet: "日期",
			Importance: 0.9, SourceType: SourceExplicit})
		_ = s.SetRelationship(ctx, key, text, 0.9)
	}

	// 7. Dated events with a short duration.
	if m := datePattern.FindStringSubmatch(text); len(m) == 4 {
		if !hasEventKeyword(text) {
			return results, nil
		}
		year, month, day := atoi(m[1]), atoi(m[2]), atoi(m[3])
		eventAt := time.Date(year, time.Month(month), day, 0, 0, 0, 0, turnAt.Location())
		addEvent(add, text, eventAt, PrecisionDate, turnAt)
		return results, nil
	}
	if hint := ParseRelativeTime(text, turnAt, turnAt.Location()); hint != nil && hint.EventAt != nil {
		if hasEventKeyword(text) {
			addEvent(add, text, time.UnixMilli(*hint.EventAt), hint.Precision, turnAt)
		}
	}

	return results, nil
}

func validNickname(name string) bool {
	if name == "" {
		return false
	}
	for _, bad := range []string{"什么", "谁", "啥", "哪", "怎么", "吗"} {
		if strings.Contains(name, bad) {
			return false
		}
	}
	return true
}

func insideQuote(text string, pos int) bool {
	for _, span := range quoteSpanPattern.FindAllStringIndex(text, -1) {
		if pos >= span[0] && pos < span[1] {
			return true
		}
	}
	return false
}

func hasEventKeyword(text string) bool {
	for _, word := range []string{"出差", "旅行", "会议", "考试", "生日", "约会", "见面", "搬家", "手术", "面试"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func addEvent(add func(AddInput), text string, eventAt time.Time, precision string, turnAt time.Time) {
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
		Text: text, Layer: LayerEpisodic, Predicate: "时间线", Facet: "事件安排",
		Importance: 0.6, EventAt: &ev, ExpiresAt: expires, EmotionalWeight: 0.3,
		EventTimePrecision: precision, EventTimeSource: "user_stated",
	})
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
