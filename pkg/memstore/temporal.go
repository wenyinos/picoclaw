package memstore

import (
	"strings"
	"time"
)

// TemporalHint is the deterministic time interpretation of a phrase. It is
// deliberately not NLP: only fixed Chinese expressions are recognized, and
// approximate phrases never get a fabricated single day.
type TemporalHint struct {
	Precision string `json:"precision"`
	EventAt   *int64 `json:"event_at,omitempty"`
	ValidFrom *int64 `json:"valid_from,omitempty"`
	ValidTo   *int64 `json:"valid_to,omitempty"`
	Source    string `json:"source"`
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func rangeHint(precision string, from, to time.Time) *TemporalHint {
	f, u := ms(from), ms(to)
	return &TemporalHint{Precision: precision, ValidFrom: &f, ValidTo: &u, Source: "user_stated"}
}

func pointHint(precision string, at time.Time) *TemporalHint {
	v := ms(at)
	return &TemporalHint{Precision: precision, EventAt: &v, Source: "user_stated"}
}

// ParseRelativeTime maps common Chinese time phrases to a precision and an
// interval. It returns nil when the phrase is narrative ("第一次", "那天") or
// contains no recognizable time expression.
func ParseRelativeTime(text string, msgAt time.Time, loc *time.Location) *TemporalHint {
	if loc == nil {
		loc = msgAt.Location()
	}
	t := msgAt.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)

	// Approximate phrases: keep the interval, never invent a day.
	if strings.Contains(text, "去年夏天") {
		y := t.Year() - 1
		return rangeHint(PrecisionApproximate,
			time.Date(y, 6, 1, 0, 0, 0, 0, loc),
			time.Date(y, 9, 1, 0, 0, 0, 0, loc))
	}
	for _, word := range []string{"前段时间", "前一阵", "之前", "以前", "最近"} {
		if strings.Contains(text, word) {
			return rangeHint(PrecisionApproximate, t.AddDate(0, -3, 0), t)
		}
	}

	// Weeks and months.
	if strings.Contains(text, "上周") || strings.Contains(text, "上星期") {
		offset := (int(t.Weekday()) + 6) % 7
		monday := day.AddDate(0, 0, -offset-7)
		return rangeHint(PrecisionRange, monday, monday.AddDate(0, 0, 7))
	}
	if strings.Contains(text, "下周") || strings.Contains(text, "下星期") {
		offset := (int(t.Weekday()) + 6) % 7
		monday := day.AddDate(0, 0, -offset+7)
		return pointHint(PrecisionPlanned, monday)
	}
	if strings.Contains(text, "上个月") || strings.Contains(text, "上月") {
		first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		return rangeHint(PrecisionRange, first.AddDate(0, -1, 0), first)
	}
	if strings.Contains(text, "下个月") || strings.Contains(text, "下月") {
		first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		return pointHint(PrecisionPlanned, first.AddDate(0, 1, 0))
	}
	if strings.Contains(text, "去年") {
		y := t.Year() - 1
		return rangeHint(PrecisionRange,
			time.Date(y, 1, 1, 0, 0, 0, 0, loc),
			time.Date(y+1, 1, 1, 0, 0, 0, 0, loc))
	}
	if strings.Contains(text, "今年") {
		return rangeHint(PrecisionRange,
			time.Date(t.Year(), 1, 1, 0, 0, 0, 0, loc),
			time.Date(t.Year()+1, 1, 1, 0, 0, 0, 0, loc))
	}

	// Days.
	switch {
	case strings.Contains(text, "前天"):
		return pointHint(PrecisionDate, day.AddDate(0, 0, -2))
	case strings.Contains(text, "昨天"):
		return pointHint(PrecisionDate, day.AddDate(0, 0, -1))
	case strings.Contains(text, "今天"):
		return pointHint(PrecisionDate, day)
	case strings.Contains(text, "后天"):
		return pointHint(PrecisionPlanned, day.AddDate(0, 0, 2))
	case strings.Contains(text, "明天"):
		return pointHint(PrecisionPlanned, day.AddDate(0, 0, 1))
	}
	return nil
}
