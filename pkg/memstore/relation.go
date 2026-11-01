package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// expandableRelations are the edge types allowed to pull context into a
// prompt. supersedes/contradicts are for inspection only: injecting them
// would surface outdated or conflicting text.
var expandableRelations = map[string]bool{
	RelRelatedTo:    true,
	RelCausedBy:     true,
	RelInfluencedBy: true,
	RelFulfilledBy:  true,
	RelNextScene:    true,
	RelPartOf:       true,
}

// AddRelation inserts a typed edge between two memories.
func (s *Store) AddRelation(ctx context.Context, r Relation) error {
	if strings.TrimSpace(r.SourceID) == "" || strings.TrimSpace(r.TargetID) == "" {
		return fmt.Errorf("memstore: relation needs source_id and target_id")
	}
	if !expandableRelations[r.RelationType] &&
		r.RelationType != RelSupersedes && r.RelationType != RelContradicts {
		return fmt.Errorf("memstore: unknown relation type %q", r.RelationType)
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = nowMillis()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertRelation(ctx, tx, r); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ListRelations returns edges that touch an entry.
func (s *Store) ListRelations(ctx context.Context, entryID string, limit int) ([]Relation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,source_id,target_id,relation_type,confidence,source_type,created_at
		 FROM memory_relations WHERE source_id=? OR target_id=?
		 ORDER BY created_at LIMIT ?`, entryID, entryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.ID, &r.SourceID, &r.TargetID, &r.RelationType,
			&r.Confidence, &r.SourceType, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExpandOneHop pulls at most perSeed related memories per seed, with a global
// cap of 12. Every target is re-checked for status, realm, scope and audience;
// there is no "retrieve first, filter later" path.
func (s *Store) ExpandOneHop(ctx context.Context, seeds []Entry, perSeed int, filter SearchOptions) ([]Entry, error) {
	if len(seeds) == 0 {
		return nil, nil
	}
	if perSeed <= 0 {
		perSeed = 3
	}
	const globalCap = 12
	seen := map[string]bool{}
	for _, seed := range seeds {
		seen[seed.ID] = true
	}
	var out []Entry
	for _, seed := range seeds {
		rows, err := s.db.QueryContext(ctx,
			`SELECT target_id, relation_type, source_type
			 FROM memory_relations
			 WHERE source_id=? AND relation_type IN ('related_to','caused_by','influenced_by','fulfilled_by','next_scene','part_of')
			 ORDER BY confidence DESC, created_at DESC LIMIT ?`, seed.ID, perSeed)
		if err != nil {
			return nil, err
		}
		type edge struct{ id, relType, sourceType string }
		var edges []edge
		for rows.Next() {
			var e edge
			if err := rows.Scan(&e.id, &e.relType, &e.sourceType); err != nil {
				_ = rows.Close()
				return nil, err
			}
			edges = append(edges, e)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, e := range edges {
			if len(out) >= globalCap {
				return out, nil
			}
			if seen[e.id] {
				continue
			}
			target, err := s.Get(ctx, e.id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if target.Status != StatusActive {
				continue
			}
			if target.Realm == RealmFictional && !filter.IncludeFic {
				continue
			}
			if target.Realm == RealmUnknown {
				continue
			}
			if filter.Realm != "" && target.Realm != filter.Realm {
				continue
			}
			if filter.Scope != "" && target.Scope != filter.Scope {
				continue
			}
			if !audienceAllowed(target.Audience, filter.Channel, filter.PrivateChannels) {
				continue
			}
			if target.Attributes == nil {
				target.Attributes = map[string]any{}
			}
			target.Attributes["relation_source"] = e.sourceType
			target.Attributes["relation_type"] = e.relType
			seen[target.ID] = true
			out = append(out, *target)
		}
	}
	return out, nil
}

// LoadCapsule renders a scene capsule in ordinal order. A fictional capsule
// returns empty as a whole for public channels or when fictional injection is
// off; individual items never leak through.
func (s *Store) LoadCapsule(
	ctx context.Context,
	clusterID, channel string,
	privateChannels []string,
	injectFictional bool,
	budget int,
) (CapsuleResult, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return CapsuleResult{}, fmt.Errorf("memstore: capsule id is required")
	}
	if budget <= 0 {
		budget = 1200
	}
	var c Cluster
	err := s.db.QueryRowContext(ctx,
		`SELECT id,realm,title,topic,status,created_at,updated_at FROM memory_clusters WHERE id=?`,
		clusterID).Scan(&c.ID, &c.Realm, &c.Title, &c.Topic, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CapsuleResult{}, fmt.Errorf("memstore: capsule %q not found", clusterID)
	}
	if err != nil {
		return CapsuleResult{}, err
	}
	result := CapsuleResult{ID: c.ID, Title: c.Title}
	if c.Realm == RealmFictional {
		if !injectFictional || !containsChannel(privateChannels, channel) {
			return result, nil
		}
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id, ordinal FROM memory_cluster_members
		 WHERE cluster_id=? ORDER BY ordinal`, clusterID)
	if err != nil {
		return CapsuleResult{}, err
	}
	type member struct {
		id      string
		ordinal int
	}
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.ordinal); err != nil {
			_ = rows.Close()
			return CapsuleResult{}, err
		}
		members = append(members, m)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return CapsuleResult{}, err
	}

	for _, m := range members {
		e, err := s.Get(ctx, m.id)
		if err != nil {
			continue
		}
		if e.Status != StatusActive {
			continue
		}
		if e.Realm == RealmFictional && !injectFictional {
			continue
		}
		if !audienceAllowed(e.Audience, channel, privateChannels) {
			continue
		}
		text := strings.TrimSpace(e.Text)
		tokens := EstimateTokens(text)
		if result.TotalTokens+tokens > budget {
			result.Truncated = true
			break
		}
		result.Items = append(result.Items, CapsuleItem{
			EntryID: e.ID, Ordinal: m.ordinal, Layer: e.Layer, Realm: e.Realm,
			Text: text, Tokens: tokens,
		})
		result.TotalTokens += tokens
	}
	return result, nil
}

// CapsuleText renders a capsule as a prompt-ready block.
func (c CapsuleResult) CapsuleText() string {
	if len(c.Items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 场景胶囊 · " + c.Title)
	for _, item := range c.Items {
		b.WriteString("\n- " + strings.TrimSpace(item.Text))
	}
	return b.String()
}

func nowMillis() int64 { return time.Now().UnixMilli() }
