package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// EstimateTokens is a cheap mixed Chinese/English token estimate.
func EstimateTokens(s string) int {
	runes := utf8.RuneCountInString(s)
	if runes == 0 {
		return 0
	}
	return (runes + 1) / 2
}

// Search scores active entries for a query and filters by audience.
func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]Entry, error) {
	where := []string{"status='active'"}
	args := []any{}
	if opts.Realm != "" {
		where = append(where, "realm=?")
		args = append(args, opts.Realm)
	} else if !opts.IncludeFic {
		where = append(where, "realm IN ('real','work')")
	}
	if opts.Layer != "" {
		where = append(where, "layer=?")
		args = append(args, opts.Layer)
	} else {
		where = append(where, "layer IN ('episodic','procedural')")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE `+strings.Join(where, " AND ")+
			` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UnixMilli()
	queryNorm := Normalize(opts.Query)
	var scored []scoredEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		if !audienceAllowed(e.Audience, opts.Channel, opts.PrivateChannels) {
			continue
		}
		recency := 1.0
		if age := now - e.UpdatedAt; age > 0 {
			recency = 1 - float64(age)/(1000*60*60*24*30)
			if recency < 0 {
				recency = 0
			}
		}
		score := e.Importance*0.6 + recency*0.2 + e.EmotionalWeight*0.2
		if queryNorm != "" {
			similarity := queryRelevance(queryNorm, e.Normalized)
			if similarity <= 0 {
				continue
			}
			score = similarity*0.6 + e.Importance*0.2 + recency*0.1 + e.EmotionalWeight*0.1
		}
		scored = append(scored, scoredEntry{entry: e, score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]Entry, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.entry)
	}
	return out, nil
}

type scoredEntry struct {
	entry Entry
	score float64
}

// BuildFixedContext renders persona, relationship, profile and boundary blocks.
// Relationship and private profile entries are only included for channels in
// privateChannels; pass an empty channel for an unfiltered administrative view.
func (s *Store) BuildFixedContext(
	ctx context.Context,
	budget int,
	injectFictional bool,
	channel string,
	privateChannels []string,
) string {
	if s == nil {
		return ""
	}
	if budget <= 0 {
		budget = 600
	}
	var parts []string
	if persona, err := s.GetActivePersona(ctx); err == nil && persona != nil {
		parts = append(parts, "## 角色设定\n"+strings.TrimSpace(persona.Content))
	}
	includeRelationship := channel == "" || containsChannel(privateChannels, channel)
	if includeRelationship {
		if rel, err := s.GetRelationship(ctx); err == nil && len(rel) > 0 {
			var b strings.Builder
			b.WriteString("## 关系状态")
			for _, r := range rel {
				b.WriteString("\n- " + r.Key + ": " + r.Value)
			}
			parts = append(parts, b.String())
		}
	}
	profile, _ := s.List(ctx, SearchOptions{
		Status: StatusActive, Layer: LayerProfile, Limit: 40,
	})
	if len(profile) > 0 {
		var b strings.Builder
		b.WriteString("## 用户画像")
		for _, e := range profile {
			if e.Realm == RealmFictional && !injectFictional {
				continue
			}
			if !audienceAllowed(e.Audience, channel, privateChannels) {
				continue
			}
			b.WriteString("\n- " + strings.TrimSpace(e.Text))
		}
		if b.Len() > len("## 用户画像") {
			parts = append(parts, b.String())
		}
	}
	boundaries, _ := s.List(ctx, SearchOptions{
		Status: StatusActive, Layer: LayerBoundary, Limit: 30,
	})
	if len(boundaries) > 0 {
		var b strings.Builder
		b.WriteString("## 边界")
		for _, e := range boundaries {
			b.WriteString("\n- " + strings.TrimSpace(e.Text))
		}
		parts = append(parts, b.String())
	}
	return trimToBudget(parts, budget, "# Memory\n\n")
}

// BuildRetrievalContext renders query-relevant episodic/procedural memories.
func (s *Store) BuildRetrievalContext(
	ctx context.Context,
	query, channel string,
	budget int,
	injectFictional bool,
	privateChannels []string,
) string {
	if s == nil || strings.TrimSpace(query) == "" {
		return ""
	}
	if budget <= 0 {
		budget = 600
	}
	entries, err := s.Search(ctx, SearchOptions{
		Query: query, Channel: channel, Limit: 200, IncludeFic: injectFictional,
		PrivateChannels: privateChannels,
	})
	if err != nil {
		return ""
	}
	var lines []string
	for _, e := range entries {
		if e.Realm == RealmFictional && !injectFictional {
			continue
		}
		if e.Realm == RealmUnknown {
			continue
		}
		line := fmt.Sprintf("- [%s/%s] %s", e.Layer, e.Realm, strings.TrimSpace(e.Text))
		if len(lines) > 0 && EstimateTokens(strings.Join(lines, "\n"))+EstimateTokens(line) > budget {
			break
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return trimToBudget([]string{"## 相关记忆\n" + strings.Join(lines, "\n")}, budget, "# Memory\n\n")
}

func trimToBudget(parts []string, budget int, header string) string {
	var b strings.Builder
	b.WriteString(header)
	used := EstimateTokens(header)
	for _, p := range parts {
		cost := EstimateTokens(p)
		if used+cost > budget {
			// Keep a truncated prefix of the first oversized block.
			if b.Len() == len(header) {
				b.WriteString(truncateByTokens(p, budget-used))
			}
			break
		}
		b.WriteString(p)
		b.WriteString("\n\n")
		used += cost
	}
	out := strings.TrimSpace(b.String())
	if out == "# Memory" {
		return ""
	}
	return out
}

func truncateByTokens(s string, budget int) string {
	if budget <= 0 {
		return ""
	}
	runes := []rune(s)
	max := budget * 2
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}

func audienceAllowed(audience, channel string, privateChannels []string) bool {
	audience = strings.TrimSpace(audience)
	if audience == "" || audience == "all" {
		return true
	}
	if audience == "private" {
		return containsChannel(privateChannels, channel)
	}
	if strings.HasPrefix(audience, "channel:") {
		return channel == strings.TrimPrefix(audience, "channel:")
	}
	return true
}

func containsChannel(list []string, channel string) bool {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return false
	}
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), channel) {
			return true
		}
	}
	return false
}
