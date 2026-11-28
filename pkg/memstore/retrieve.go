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
	if err := s.checkReady(); err != nil {
		return nil, err
	}

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

// retrieveCandidates merges the four retrieval channels. Permission filters
// (status/realm/scope/audience/domain) run in SQL before LIMIT, so an
// invisible candidate can never crowd out a visible memory (B07).
func (s *Store) retrieveCandidates(ctx context.Context, opts SearchOptions, admin bool) ([]scoredEntry, error) {
	where := []string{"1=1"}
	args := []any{}

	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = StatusActive
	}
	if !strings.EqualFold(status, "all") {
		where = append(where, "e.status=?")
		args = append(args, status)
	}
	if opts.Realm != "" {
		where = append(where, "e.realm=?")
		args = append(args, opts.Realm)
	}
	if !admin && !opts.IncludeFic {
		where = append(where, "e.realm IN ('real','work')")
	}
	// Group and public chats only see real, explicitly shared facts. This is an
	// intersection: an explicit work/fictional realm above cannot widen it.
	if isSharedChatKind(opts.ChatKindHint) {
		where = append(where, "e.realm='real'")
	}
	if opts.Layer != "" {
		where = append(where, "e.layer=?")
		args = append(args, opts.Layer)
	} else if !admin && !opts.AllLayers {
		where = append(where, "e.layer IN ('relationship','episodic','procedural')")
	}
	if opts.Predicate != "" {
		where = append(where, "e.predicate=?")
		args = append(args, opts.Predicate)
	}
	if opts.Facet != "" {
		where = append(where, "e.facet=?")
		args = append(args, opts.Facet)
	}
	if opts.Scope != "" {
		where = append(where, "e.scope=?")
		args = append(args, opts.Scope)
	}
	if opts.Audience != "" {
		where = append(where, "e.audience=?")
		args = append(args, opts.Audience)
	}
	if !admin || opts.ExcludeExpired {
		if opts.ExcludeExpired {
			where = append(where, "(e.expires_at IS NULL OR e.expires_at > ?)")
			args = append(args, s.now())
		}
	}
	if !admin {
		// Empty domain is not "all domains": callers without an ownership
		// binding read only the isolated legacy partition.
		domainID := strings.TrimSpace(opts.DomainID)
		if domainID == "" {
			domainID = DomainLegacyUnassigned
		}
		where = append(where, "e.domain_id=?")
		args = append(args, domainID)

		audience, audienceArgs := audienceSQLFor(opts.ChatKindHint, opts.Channel, opts.PrivateChannelExclusions)
		where = append(where, audience)
		args = append(args, audienceArgs...)
	}
	base := strings.Join(where, " AND ")

	byID := map[string]Entry{}
	addEntry := func(e Entry) { byID[e.ID] = e }
	temporalIDs := map[string]bool{}

	// Channel 1: anchor. Visible rows are selected in SQL first; the limit is
	// a scan guard, not a visibility cut.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+entryColumns+` FROM entries e WHERE `+base+` ORDER BY e.updated_at DESC LIMIT 800`, args...)
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
			hits, err := s.searchFTS(ctx, opts.Query, 100, base, args)
			if err == nil {
				ftsHits = len(hits)
				for _, hit := range hits {
					if _, ok := byID[hit.EntryID]; ok {
						continue
					}
					if e, err := s.loadOne(ctx, hit.EntryID, base, args); err == nil {
						addEntry(*e)
					}
				}
			}
		}
		// Channel 3: LIKE. Short queries always use it; longer queries keep a
		// real fallback too (B09). The filter runs in SQL, so the LIMIT counts
		// visible rows.
		if len(likeTerms) > 0 || ftsHits < 5 {
			for _, term := range likeTerms {
				ids, err := s.searchLike(ctx, term, 100, base, args)
				if err != nil {
					return nil, err
				}
				for _, id := range ids {
					if _, ok := byID[id]; ok {
						continue
					}
					if e, err := s.loadOne(ctx, id, base, args); err == nil {
						addEntry(*e)
					}
				}
			}
		}
		// Channel 4: temporal overlap. A pure time hit is a first-class
		// relevance signal, not a keyword by-product (B08).
		if hint := ParseRelativeTime(opts.Query, time.Now(), time.Local); hint != nil {
			from, to := hintRange(hint)
			if from != 0 && to > from {
				tq := `SELECT ` + entryColumns + ` FROM entries e WHERE ` + base + ` AND (
					(e.event_at IS NOT NULL AND e.event_at >= ? AND e.event_at < ?)
					OR (e.event_start IS NOT NULL AND e.event_start < ? AND COALESCE(e.event_end, ?) > ?)
					OR (e.valid_from IS NOT NULL AND e.valid_from < ? AND COALESCE(e.valid_to, ?) > ?))`
				targs := append(append([]any{}, args...), from, to, to, to, from, to, to, from)
				trows, err := s.db.QueryContext(ctx, tq, targs...)
				if err == nil {
					for trows.Next() {
						e, err := scanEntry(trows)
						if err != nil {
							break
						}
						addEntry(e)
						temporalIDs[e.ID] = true
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
			if sim <= 0 && anchor <= 0 && !temporalIDs[e.ID] {
				continue
			}
			timeBoost := 0.0
			if temporalIDs[e.ID] {
				timeBoost = 0.25
			}
			score = sim*0.50 + anchor*0.15 + timeBoost + e.Importance*0.20 +
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
		`SELECT `+entryColumns+` FROM entries e WHERE e.id=? AND `+base, append([]any{id}, args...)...)
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

// BuildFixedContext renders the fixed blocks for a private-chat caller. It is
// kept for CLI/tests; request paths must pass the trusted chat kind through
// BuildFixedContextFor so a group chat cannot inherit private blocks.
func (s *Store) BuildFixedContext(
	ctx context.Context,
	budget int,
	injectFictional bool,
	channel string,
	privateChannels []string,
) (string, FixedUsage) {
	return s.BuildFixedContextFor(ctx, budget, injectFictional, channel, "", privateChannels)
}

// BuildFixedContextFor renders persona, relationship, profile and boundary
// blocks and returns the real token usage. Group/public chat kinds only receive
// audience ""/"all" content; relationship state is omitted entirely.
func (s *Store) BuildFixedContextFor(
	ctx context.Context,
	budget int,
	injectFictional bool,
	channel, chatKind string,
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
	if persona, err := s.GetActivePersonaForChat(ctx, channel, chatKind, privateChannels, injectFictional); err == nil && persona != nil {
		parts = append(parts, "## 角色设定\n"+strings.TrimSpace(persona.Content))
	}
	includeRelationship := chatKindAllowsPrivate(chatKind) &&
		(channel == "" || channelIsPrivate(channel, privateChannels))
	if includeRelationship {
		if rel, err := s.GetRelationship(ctx); err == nil && len(rel) > 0 {
			var b strings.Builder
			b.WriteString("## 关系状态")
			for _, r := range rel {
				if !audienceAllowedForChat(r.Audience, chatKind, channel, privateChannels) {
					continue
				}
				b.WriteString("\n- " + r.Key + ": " + r.Value)
			}
			if b.Len() > len("## 关系状态") {
				parts = append(parts, b.String())
			}
		}
	}
	profile, _ := s.List(ctx, SearchOptions{
		Status: StatusActive, Layer: LayerProfile, Limit: 40,
		DomainID: DomainLegacyUnassigned, Channel: channel, ChatKindHint: chatKind,
		PrivateChannelExclusions: privateChannels, ExcludeExpired: true,
	})
	now := s.now()
	var profileLines []string
	for _, e := range profile {
		// TTL is a usage limit, not just an archive trigger (B08): an expired
		// profile row must not be injected while it still exists.
		if e.ExpiresAt != nil && *e.ExpiresAt <= now {
			continue
		}
		if e.Realm == RealmFictional && !injectFictional {
			continue
		}
		if !audienceAllowedForChat(e.Audience, chatKind, channel, privateChannels) {
			continue
		}
		profileLines = append(profileLines, "- "+strings.TrimSpace(e.Text))
	}
	if len(profileLines) > 0 {
		parts = append(parts, "## 用户画像\n"+strings.Join(profileLines, "\n"))
	}

	// Base content may not consume the whole fixed block; boundaries need an
	// explicit share and everything is counted against one target.
	baseBudget := budget * 70 / 100
	if baseBudget < 100 {
		baseBudget = 100
	}
	base := trimToBudget(parts, baseBudget, "# Memory\n\n")
	usage.BaseTokens = EstimateTokens(base)

	boundaryBudget := budget - usage.BaseTokens
	if boundaryBudget < 0 {
		boundaryBudget = 0
	}
	usage.BoundaryBudget = boundaryBudget
	boundaries, _ := s.List(ctx, SearchOptions{
		Status: StatusActive, Layer: LayerBoundary, Limit: 30,
		DomainID: DomainLegacyUnassigned, Channel: channel, ChatKindHint: chatKind,
		PrivateChannelExclusions: privateChannels, ExcludeExpired: true,
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
		if !audienceAllowedForChat(e.Audience, chatKind, channel, privateChannels) {
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
	if usage.BoundaryTokens > usage.BoundaryBudget {
		usage.OverflowTokens = usage.BoundaryTokens - usage.BoundaryBudget
		usage.CoreOverflow = true
	}

	if boundaryText == "" {
		return base, usage
	}
	if base == "" {
		return "# Memory\n\n" + boundaryText, usage
	}
	return base + "\n\n" + boundaryText, usage
}

// BuildRetrievalContext renders the retrieval block for a private-chat caller.
// Request paths must use BuildRetrievalContextFor with the trusted chat kind.
func (s *Store) BuildRetrievalContext(
	ctx context.Context,
	query, channel string,
	budget int,
	injectFictional bool,
	privateChannels []string,
) string {
	return s.BuildRetrievalContextFor(ctx, query, channel, "", budget, injectFictional, privateChannels)
}

// BuildRetrievalContextFor renders query-relevant relationship, episodic and
// procedural memories, plus one-hop related memories. Relationship entries are
// capped so they cannot crowd out event/procedure memories.
func (s *Store) BuildRetrievalContextFor(
	ctx context.Context,
	query, channel, chatKind string,
	budget int,
	injectFictional bool,
	privateChannels []string,
) string {
	if s == nil || strings.TrimSpace(query) == "" || budget <= 0 {
		return ""
	}
	opts := SearchOptions{
		Query: query, Channel: channel, Limit: 200, IncludeFic: injectFictional,
		PrivateChannelExclusions: privateChannels,
		ChatKindHint:             chatKind,
		DomainID:                 DomainLegacyUnassigned,
		ExcludeExpired:           true,
	}
	entries, err := s.Search(ctx, opts)
	if err != nil {
		return ""
	}
	var lines []string
	used := map[string]bool{}
	relationshipCount := 0
	otherCount := 0
	usedTokens := 0
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
		cost := EstimateTokens(line) + EstimateTokens("\n")
		if usedTokens+cost > budget {
			break
		}
		lines = append(lines, line)
		used[e.ID] = true
		usedTokens += cost
	}

	// One-hop expansion adds shared-experience context that keyword search
	// misses. It shares the layer quotas and the ID set with the main pass:
	// expansion can neither duplicate an injected entry nor exceed the total
	// quota (B10).
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
			if used[e.ID] {
				continue
			}
			if e.Layer == LayerRelationship {
				if relationshipCount >= 2 {
					continue
				}
				relationshipCount++
			} else {
				if otherCount >= 6 {
					continue
				}
				otherCount++
			}
			text := strings.TrimSpace(e.Text)
			line := fmt.Sprintf("- [关联/%s] %s", e.Layer, text)
			cost := EstimateTokens(line) + EstimateTokens("\n")
			if usedTokens+cost > budget {
				break
			}
			lines = append(lines, line)
			used[e.ID] = true
			usedTokens += cost
			relatedCount++
		}
	}

	if len(lines) == 0 {
		return ""
	}
	usedIDs := make([]string, 0, len(used))
	for id := range used {
		usedIDs = append(usedIDs, id)
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
		// Separators are real characters in the final prompt: count them, or
		// the rendered block drifts over the declared budget (B11).
		cost := EstimateTokens(p) + EstimateTokens("\n\n")
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

func audienceAllowed(audience, channel string, privateExclusions []string) bool {
	audience = strings.TrimSpace(audience)
	if audience == "" || audience == "all" {
		return true
	}
	if audience == "private" {
		return channelIsPrivate(channel, privateExclusions)
	}
	if strings.HasPrefix(audience, "channel:") {
		return channel == strings.TrimPrefix(audience, "channel:")
	}
	// Unknown audiences are not public by default; only an explicit shared
	// marker passes.
	return false
}

// isSharedChatKind reports whether a context belongs to a shared conversation.
// Missing/unknown kinds are deliberately not treated as shared here: request
// entry points normalize unknown values to public before reaching the Store.
func isSharedChatKind(chatKind string) bool {
	switch strings.ToLower(strings.TrimSpace(chatKind)) {
	case ChatKindGroup, ChatKindPublic:
		return true
	default:
		return false
	}
}

// chatKindAllowsPrivate reports whether a trusted chat kind may read private
// content. Empty keeps CLI/maintenance compatibility; group and public never do.
func chatKindAllowsPrivate(chatKind string) bool {
	switch strings.ToLower(strings.TrimSpace(chatKind)) {
	case ChatKindGroup, ChatKindPublic:
		return false
	default:
		return true
	}
}

// audienceAllowedForChat is audienceAllowed with the chat kind applied first:
// a group/public chat only sees audience ""/"all", even on a private channel.
func audienceAllowedForChat(audience, chatKind, channel string, privateExclusions []string) bool {
	if !chatKindAllowsPrivate(chatKind) {
		a := strings.TrimSpace(audience)
		return a == "" || a == "all"
	}
	return audienceAllowed(audience, channel, privateExclusions)
}

// audienceSQL is the SQL twin of audienceAllowed. It runs inside every
// retrieval query so LIMIT counts visible rows (B07). Unknown audience values
// fail closed: a typo must not expose a private memory.
func audienceSQL(channel string, privateExclusions []string) (string, []any) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return `(e.audience IN ('','all'))`, nil
	}
	if channelIsPrivate(channel, privateExclusions) {
		return `(e.audience IN ('','all','private') OR e.audience = ?)`, []any{"channel:" + channel}
	}
	return `(e.audience IN ('','all') OR e.audience = ?)`, []any{"channel:" + channel}
}

// audienceSQLFor additionally respects the chat kind: a group or public chat
// never reads private memory, even when the channel is not excluded.
func audienceSQLFor(chatKind, channel string, privateExclusions []string) (string, []any) {
	switch chatKind {
	case ChatKindGroup, ChatKindPublic:
		return `(e.audience IN ('','all'))`, nil
	}
	return audienceSQL(channel, privateExclusions)
}

// channelIsPrivate reports whether one channel may read private memories.
// Since 0.7.3 every channel is private by default; the owner opts out per
// channel through an exclusion list instead of maintaining an allow-list.
// An empty channel is never private: without a channel identity the caller
// gets the conservative public view.
func channelIsPrivate(channel string, exclusions []string) bool {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return false
	}
	for _, excluded := range exclusions {
		if strings.EqualFold(strings.TrimSpace(excluded), channel) {
			return false
		}
	}
	return true
}
