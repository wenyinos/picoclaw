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

// Search scores active entries for a query using the hybrid channels
// (anchor, FTS, LIKE, temporal) and returns the top matches.
func (s *Store) Search(ctx context.Context, opts SearchOptions) ([]Entry, error) {
	scored, err := s.retrieveCandidates(ctx, opts, false)
	if err != nil {
		return nil, err
	}
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 300
	}
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]Entry, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.entry)
	}
	return out, nil
}

type scoredEntry struct {
	entry  Entry
	score  float64
	anchor float64
	pinned bool
}

// retrieveCandidates merges the four retrieval channels. Every channel runs
// with status/realm/scope filters in SQL and the audience filter in Go before
// scoring; there is no path that recalls first and filters later.
func (s *Store) retrieveCandidates(ctx context.Context, opts SearchOptions, admin bool) ([]scoredEntry, error) {
	where := []string{"1=1"}
	args := []any{}

	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = StatusActive
	}
	if !strings.EqualFold(status, "all") {
		where = append(where, "status=?")
		args = append(args, status)
	}
	if opts.Realm != "" {
		where = append(where, "realm=?")
		args = append(args, opts.Realm)
	} else if !admin && !opts.IncludeFic {
		where = append(where, "realm IN ('real','work')")
	}
	if opts.Layer != "" {
		where = append(where, "layer=?")
		args = append(args, opts.Layer)
	} else if !admin {
		where = append(where, "layer IN ('relationship','episodic','procedural')")
	}
	if opts.Predicate != "" {
		where = append(where, "predicate=?")
		args = append(args, opts.Predicate)
	}
	if opts.Facet != "" {
		where = append(where, "facet=?")
		args = append(args, opts.Facet)
	}
	if opts.Scope != "" {
		where = append(where, "scope=?")
		args = append(args, opts.Scope)
	}
	if opts.Audience != "" {
		where = append(where, "audience=?")
		args = append(args, opts.Audience)
	}
	base := strings.Join(where, " AND ")

	byID := map[string]Entry{}
	addEntry := func(e Entry) { byID[e.ID] = e }

	// Channel 1: anchor. A small store is scanned directly; anchor hits are
	// exact/contains matches on facet, predicate, subject and title.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE `+base+` ORDER BY updated_at DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	queryNorm := Normalize(opts.Query)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !admin && !audienceAllowed(e.Audience, opts.Channel, opts.PrivateChannels) {
			continue
		}
		addEntry(e)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if queryNorm != "" {
		// Channel 2: FTS, only when the phrase has at least three runes.
		match, likeTerms := buildFTSQuery(opts.Query)
		ftsHits := 0
		if match != "" && s.ftsTableExists(ctx) {
			hits, err := s.searchFTS(ctx, opts.Query, 100)
			if err == nil {
				ftsHits = len(hits)
				for _, hit := range hits {
					if _, ok := byID[hit.EntryID]; ok {
						continue
					}
					if e, err := s.loadOne(ctx, hit.EntryID, base, args); err == nil {
						if !admin && !audienceAllowed(e.Audience, opts.Channel, opts.PrivateChannels) {
							continue
						}
						addEntry(*e)
					}
				}
			}
		}
		// Channel 3: LIKE. Short queries always use it; longer queries fall
		// back when FTS recall is thin.
		if len(likeTerms) > 0 || ftsHits < 5 {
			for _, term := range likeTerms {
				ids, err := s.searchLike(ctx, term, 100)
				if err != nil {
					return nil, err
				}
				for _, id := range ids {
					if _, ok := byID[id]; ok {
						continue
					}
					if e, err := s.loadOne(ctx, id, base, args); err == nil {
						if !admin && !audienceAllowed(e.Audience, opts.Channel, opts.PrivateChannels) {
							continue
						}
						addEntry(*e)
					}
				}
			}
		}
		// Channel 4: temporal overlap.
		if hint := ParseRelativeTime(opts.Query, time.Now(), time.Local); hint != nil {
			from, to := hintRange(hint)
			if from != 0 && to > from {
				tq := `SELECT ` + entryColumns + ` FROM entries WHERE ` + base + ` AND (
					(event_at IS NOT NULL AND event_at >= ? AND event_at < ?)
					OR (valid_from IS NOT NULL AND valid_from < ? AND COALESCE(valid_to, ?) > ?))`
				targs := append(append([]any{}, args...), from, to, to, to, from)
				trows, err := s.db.QueryContext(ctx, tq, targs...)
				if err == nil {
					for trows.Next() {
						e, err := scanEntry(trows)
						if err != nil {
							break
						}
						if admin || audienceAllowed(e.Audience, opts.Channel, opts.PrivateChannels) {
							addEntry(e)
						}
					}
					_ = trows.Close()
				}
			}
		}
	}

	now := time.Now().UnixMilli()
	scored := make([]scoredEntry, 0, len(byID))
	for _, e := range byID {
		anchor := anchorBoost(e, queryNorm)
		pinned := e.PinnedCore && anchor >= 1.0
		score := 0.0
		if queryNorm == "" {
			score = e.Importance*0.6 + recencyScore(entryTime(e), now)*0.2 + e.EmotionalWeight*0.2
		} else {
			sim := queryRelevance(queryNorm, e.Normalized)
			// A long text that mentions the anchor once yields a tiny Jaccard
			// score, so the facet/predicate match must be a max, not a
			// fallback only when similarity is exactly zero.
			if facetSim := queryRelevance(queryNorm, Normalize(e.Predicate+" "+e.Facet)); facetSim > sim {
				sim = facetSim
			}
			if sim <= 0 && anchor <= 0 {
				continue
			}
			score = sim*0.50 + anchor*0.15 + e.Importance*0.20 +
				recencyScore(entryTime(e), now)*0.10 + e.EmotionalWeight*0.05
		}
		scored = append(scored, scoredEntry{entry: e, score: score, anchor: anchor, pinned: pinned})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].pinned != scored[j].pinned {
			return scored[i].pinned
		}
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].entry.UpdatedAt != scored[j].entry.UpdatedAt {
			return scored[i].entry.UpdatedAt > scored[j].entry.UpdatedAt
		}
		return scored[i].entry.ID < scored[j].entry.ID
	})
	return dedupeScored(scored), nil
}

func (s *Store) loadOne(ctx context.Context, id, base string, args []any) (*Entry, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+entryColumns+` FROM entries WHERE id=? AND `+base, append([]any{id}, args...)...)
	e, err := scanEntry(row)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func hintRange(h *TemporalHint) (int64, int64) {
	if h == nil {
		return 0, 0
	}
	if h.ValidFrom != nil && h.ValidTo != nil {
		return *h.ValidFrom, *h.ValidTo
	}
	if h.EventAt != nil {
		return *h.EventAt, *h.EventAt + 24*60*60*1000
	}
	return 0, 0
}

// anchorBoost makes facet/predicate matches rank above importance. Without it
// a 0.95-importance noise entry outranks a 0.80 T0 rule.
func anchorBoost(e Entry, queryNorm string) float64 {
	if queryNorm == "" {
		return 0
	}
	facet := Normalize(e.Facet)
	predicate := Normalize(e.Predicate)
	subject := Normalize(e.Subject)
	switch {
	case facet != "" && facet == queryNorm:
		return 1.0
	case predicate != "" && predicate == queryNorm:
		return 0.8
	case titleContains(e.Text, queryNorm):
		return 0.6
	case facet != "" && strings.Contains(facet, queryNorm):
		return 0.4
	case predicate != "" && strings.Contains(predicate, queryNorm):
		return 0.4
	case subject != "" && strings.Contains(subject, queryNorm):
		return 0.4
	}
	return 0
}

// titleContains checks the first-line 【...】 marker used by the reference
// database for scene titles.
func titleContains(text, queryNorm string) bool {
	line := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		line = text[:i]
	}
	start := strings.Index(line, "【")
	end := strings.Index(line, "】")
	if start < 0 || end <= start {
		return false
	}
	return strings.Contains(Normalize(line[start+1:end]), queryNorm)
}

func entryTime(e Entry) int64 {
	if e.EventAt != nil {
		return *e.EventAt
	}
	return e.UpdatedAt
}

// dedupeScored keeps the best item per (realm, subject, predicate, facet) and
// per near-identical text. Pinned entries are never dropped.
func dedupeScored(in []scoredEntry) []scoredEntry {
	out := make([]scoredEntry, 0, len(in))
	keys := map[string]bool{}
	for _, item := range in {
		if item.pinned {
			out = append(out, item)
			continue
		}
		key := item.entry.Realm + "\x00" + item.entry.Subject + "\x00" +
			item.entry.Predicate + "\x00" + item.entry.Facet
		if item.entry.Facet != "" && keys[key] {
			continue
		}
		dup := false
		for _, kept := range out {
			if kept.pinned {
				continue
			}
			if Jaccard(item.entry.Normalized, kept.entry.Normalized) >= 0.85 {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if item.entry.Facet != "" {
			keys[key] = true
		}
		out = append(out, item)
	}
	return out
}

// BuildFixedContext renders persona, relationship, profile and boundary blocks
// and returns the real token usage. Relationship and private profile entries
// are only included for channels in privateChannels; pass an empty channel for
// an unfiltered administrative view.
func (s *Store) BuildFixedContext(
	ctx context.Context,
	budget int,
	injectFictional bool,
	channel string,
	privateChannels []string,
) (string, FixedUsage) {
	var usage FixedUsage
	if s == nil {
		return "", usage
	}
	if budget <= 0 {
		budget = 600
	}
	var parts []string
	if persona, err := s.GetActivePersonaForChannel(ctx, channel, privateChannels, injectFictional); err == nil && persona != nil {
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

	base := trimToBudget(parts, budget, "# Memory\n\n")
	usage.BaseTokens = EstimateTokens(base)

	// Boundaries are a safety block: they get their own cap and are counted
	// in the total, instead of silently doubling the fixed budget.
	boundaryBudget := budget * 30 / 100
	if boundaryBudget < 200 {
		boundaryBudget = 200
	}
	usage.BoundaryBudget = boundaryBudget
	boundaries, _ := s.List(ctx, SearchOptions{
		Status: StatusActive, Layer: LayerBoundary, Limit: 30,
	})
	sort.SliceStable(boundaries, func(i, j int) bool {
		if boundaries[i].PinnedCore != boundaries[j].PinnedCore {
			return boundaries[i].PinnedCore
		}
		return boundaries[i].Importance > boundaries[j].Importance
	})
	var b strings.Builder
	b.WriteString("## 边界")
	boundaryTokens := EstimateTokens("## 边界")
	added := 0
	for _, e := range boundaries {
		if e.Realm == RealmFictional && !injectFictional {
			continue
		}
		if !audienceAllowed(e.Audience, channel, privateChannels) {
			continue
		}
		line := "\n- " + strings.TrimSpace(e.Text)
		cost := EstimateTokens(line)
		// Always keep at least one boundary line, even when it alone exceeds
		// the cap: safety rules outrank the budget.
		if added > 0 && boundaryTokens+cost > boundaryBudget {
			usage.BoundaryCut++
			continue
		}
		b.WriteString(line)
		boundaryTokens += cost
		added++
	}
	boundaryText := ""
	if added > 0 {
		boundaryText = b.String()
		usage.BoundaryTokens = boundaryTokens
	}
	usage.TotalTokens = usage.BaseTokens + usage.BoundaryTokens

	if boundaryText == "" {
		return base, usage
	}
	if base == "" {
		return "# Memory\n\n" + boundaryText, usage
	}
	return base + "\n\n" + boundaryText, usage
}

// BuildRetrievalContext renders query-relevant relationship, episodic and
// procedural memories, plus one-hop related memories. Relationship entries are
// capped so they cannot crowd out event/procedure memories.
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
	opts := SearchOptions{
		Query: query, Channel: channel, Limit: 200, IncludeFic: injectFictional,
		PrivateChannels: privateChannels,
	}
	entries, err := s.Search(ctx, opts)
	if err != nil {
		return ""
	}
	var lines []string
	var usedIDs []string
	relationshipCount := 0
	otherCount := 0
	for _, e := range entries {
		if e.Realm == RealmFictional && !injectFictional {
			continue
		}
		if e.Realm == RealmUnknown {
			continue
		}
		switch e.Layer {
		case LayerRelationship:
			if relationshipCount >= 2 {
				continue
			}
			relationshipCount++
		default:
			if otherCount >= 6 {
				continue
			}
			otherCount++
		}
		text := strings.TrimSpace(e.Text)
		if e.Layer == LayerRelationship && utf8.RuneCountInString(text) > 600 {
			text = truncateRunes(text, 600)
		}
		line := fmt.Sprintf("- [%s/%s] %s", e.Layer, e.Realm, text)
		if len(lines) > 0 && EstimateTokens(strings.Join(lines, "\n"))+EstimateTokens(line) > budget {
			break
		}
		lines = append(lines, line)
		usedIDs = append(usedIDs, e.ID)
	}

	// One-hop expansion adds shared-experience context that keyword search
	// misses. It is additive and still respects the budget.
	if len(entries) > 0 {
		seedCount := len(entries)
		if seedCount > 3 {
			seedCount = 3
		}
		related, _ := s.ExpandOneHop(ctx, entries[:seedCount], 3, opts)
		relatedCount := 0
		for _, e := range related {
			if relatedCount >= 3 {
				break
			}
			text := strings.TrimSpace(e.Text)
			line := fmt.Sprintf("- [关联/%s] %s", e.Layer, text)
			if EstimateTokens(strings.Join(lines, "\n"))+EstimateTokens(line) > budget {
				break
			}
			lines = append(lines, line)
			usedIDs = append(usedIDs, e.ID)
			relatedCount++
		}
	}

	if len(lines) == 0 {
		return ""
	}
	s.RecordUsage(usedIDs)
	return trimToBudget([]string{"## 相关记忆\n" + strings.Join(lines, "\n")}, budget, "# Memory\n\n")
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
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
