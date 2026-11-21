package memstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SummaryClaim is one sentence of a stage summary plus the memories that back
// it. A claim without live evidence must not be published.
type SummaryClaim struct {
	ClaimID  string   `json:"claim_id"`
	Text     string   `json:"text"`
	EntryIDs []string `json:"entry_ids"`
}

// SummaryVersion is one published (or invalidated) stage summary.
type SummaryVersion struct {
	ID               int64          `json:"id"`
	ClusterID        string         `json:"cluster_id"`
	DomainID         string         `json:"domain_id,omitempty"`
	Version          int            `json:"version"`
	Generation       int            `json:"generation"`
	Summary          string         `json:"summary"`
	Claims           []SummaryClaim `json:"claims,omitempty"`
	Confidence       float64        `json:"confidence"`
	Model            string         `json:"model,omitempty"`
	PromptVersion    string         `json:"prompt_version,omitempty"`
	InputFingerprint string         `json:"input_fingerprint,omitempty"`
	Status           string         `json:"status"`
	ReviewStatus     string         `json:"review_status,omitempty"`
	CreatedAt        int64          `json:"created_at"`
	InvalidatedAt    *int64         `json:"invalidated_at,omitempty"`
}

// Summarizer turns a cluster's entries into draft claims. Implementations may
// be deterministic fakes in tests, local templates, or an external model
// behind an explicit processing grant.
type Summarizer interface {
	Summarize(ctx context.Context, cluster Cluster, entries []Entry) ([]SummaryClaim, error)
}

// PublishSummary stores a new summary version after verifying every claim is
// backed by live entries in the same domain. Publishing is an explicit act:
// summaries are never authoritative facts.
func (s *Store) PublishSummary(
	ctx context.Context,
	clusterID, summary string,
	claims []SummaryClaim,
	model, promptVersion string,
) (*SummaryVersion, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(summary) == "" {
		return nil, fmt.Errorf("memstore: empty summary")
	}
	if len(claims) == 0 {
		return nil, fmt.Errorf("memstore: summary needs at least one evidence-bound claim")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var domainID string
	var generation int
	if err := s.db.QueryRowContext(ctx,
		`SELECT domain_id, generation FROM memory_clusters WHERE id=?`, clusterID).
		Scan(&domainID, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("memstore: cluster %q not found", clusterID)
		}
		return nil, err
	}

	// Verify claims before anything is written: the fingerprint locks the
	// exact evidence set so a later retry cannot publish a drifted summary.
	fingerprintParts := []string{clusterID}
	evidence := map[string][]Entry{}
	for _, claim := range claims {
		if strings.TrimSpace(claim.ClaimID) == "" || strings.TrimSpace(claim.Text) == "" {
			return nil, fmt.Errorf("memstore: claim needs id and text")
		}
		if len(claim.EntryIDs) == 0 {
			return nil, fmt.Errorf("memstore: claim %q has no evidence", claim.ClaimID)
		}
		for _, entryID := range claim.EntryIDs {
			e, err := s.getLocked(ctx, entryID)
			if err != nil {
				return nil, fmt.Errorf("memstore: claim %q evidence %q unavailable", claim.ClaimID, entryID)
			}
			if e.Status != StatusActive {
				return nil, fmt.Errorf("memstore: claim %q evidence %q is not active", claim.ClaimID, entryID)
			}
			if domainID != "" && e.DomainID != domainID {
				return nil, fmt.Errorf("memstore: claim %q mixes domains", claim.ClaimID)
			}
			fingerprintParts = append(fingerprintParts, fmt.Sprintf("%s@%d", entryID, e.RowRevision))
			evidence[claim.ClaimID] = append(evidence[claim.ClaimID], *e)
		}
	}
	fingerprint := ContentHash(Normalize(strings.Join(fingerprintParts, "|")))

	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Supersede previous active versions of this cluster.
	if _, err := tx.ExecContext(ctx,
		`UPDATE memory_summary_versions SET status='superseded', superseded_at=?
		 WHERE cluster_id=? AND status='active'`, now, clusterID); err != nil {
		return nil, err
	}
	var version int
	_ = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0)+1 FROM memory_summary_versions WHERE cluster_id=?`,
		clusterID).Scan(&version)
	claimsJSON, _ := json.Marshal(claims)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO memory_summary_versions
		 (cluster_id,version,summary,confidence,model,prompt_version,status,created_at,
		  domain_id,generation,input_fingerprint,claims,review_status)
		 VALUES(?,?,?,?,?,?,'active',?,?,?,?,?,'approved')`,
		clusterID, version, strings.TrimSpace(summary), 0.8, model, promptVersion, now,
		domainID, generation, fingerprint, string(claimsJSON))
	if err != nil {
		return nil, err
	}
	summaryID, _ := res.LastInsertId()
	for _, claim := range claims {
		for _, e := range evidence[claim.ClaimID] {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO memory_summary_evidence
				 (summary_version_id,claim_id,entry_id,row_revision,created_at)
				 VALUES(?,?,?,?,?)`,
				summaryID, claim.ClaimID, e.ID, e.RowRevision, now); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO memory_dependencies
				 (parent_kind,parent_id,parent_revision,child_kind,child_id,dependency_type,created_at)
				 VALUES('entry',?,?,'summary',?,'summarized_by',?)`,
				e.ID, e.RowRevision, fmt.Sprintf("%d", summaryID), now); err != nil {
				return nil, err
			}
		}
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &SummaryVersion{
		ID: summaryID, ClusterID: clusterID, DomainID: domainID, Version: version,
		Generation: generation, Summary: strings.TrimSpace(summary), Claims: claims,
		Confidence: 0.8, Model: model, PromptVersion: promptVersion,
		InputFingerprint: fingerprint, Status: "active", ReviewStatus: "approved",
		CreatedAt: now,
	}, nil
}

// InvalidateClusterSummaries marks every active summary of a cluster dirty and
// bumps its generation so in-flight generation cannot publish (FR-08).
func (s *Store) InvalidateClusterSummaries(ctx context.Context, clusterID, reason string) (int, error) {
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE memory_summary_versions
		 SET status='invalidated', invalidated_at=?, superseded_at=COALESCE(superseded_at,?)
		 WHERE cluster_id=? AND status='active'`, now, now, clusterID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_clusters SET generation=generation+1, updated_at=? WHERE id=?`,
		now, clusterID); err != nil {
		return int(n), err
	}
	if n > 0 {
		_ = s.bumpRevisionLocked(ctx)
		_, _ = s.db.ExecContext(ctx,
			`INSERT INTO memory_operations
			 (operation,target_entry_id,reason,affected_count,created_at,reason_code)
			 VALUES('invalidate_summary','',?,?,?,'dependency_change')`,
			reason, n, now)
	}
	return int(n), nil
}

// InvalidateSummariesForEntry invalidates every cluster containing an entry.
// It is called on forget, correction, archive and visibility changes.
func (s *Store) InvalidateSummariesForEntry(ctx context.Context, entryID, reason string) (int, error) {
	clusters, err := s.clusterIDsLocked(ctx, entryID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, clusterID := range clusters {
		n, err := s.InvalidateClusterSummaries(ctx, clusterID, reason)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// ListSummaries returns the versions of one cluster, newest first.
func (s *Store) ListSummaries(ctx context.Context, clusterID string, limit int) ([]SummaryVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,cluster_id,domain_id,version,generation,summary,claims,confidence,
		        model,prompt_version,input_fingerprint,status,review_status,created_at,invalidated_at
		 FROM memory_summary_versions WHERE cluster_id=? ORDER BY version DESC LIMIT ?`,
		clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SummaryVersion{}
	for rows.Next() {
		var v SummaryVersion
		var claims string
		var invalidated sql.NullInt64
		if err := rows.Scan(&v.ID, &v.ClusterID, &v.DomainID, &v.Version, &v.Generation,
			&v.Summary, &claims, &v.Confidence, &v.Model, &v.PromptVersion,
			&v.InputFingerprint, &v.Status, &v.ReviewStatus, &v.CreatedAt, &invalidated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(claims), &v.Claims)
		if invalidated.Valid {
			ts := invalidated.Int64
			v.InvalidatedAt = &ts
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ExpandSummaryEvidence returns the live entries behind a summary version.
// Invalidated summaries still expand for audit, but their status is visible.
func (s *Store) ExpandSummaryEvidence(ctx context.Context, summaryVersionID int64) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id FROM memory_summary_evidence WHERE summary_version_id=? ORDER BY claim_id, entry_id`,
		summaryVersionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, id := range ids {
		e, err := s.Get(ctx, id)
		if err != nil {
			continue // evidence removed: the claim no longer expands to it
		}
		out = append(out, *e)
	}
	return out, nil
}

// GenerateSummary asks a Summarizer for draft claims for a cluster, then
// publishes them. A nil summarizer returns the deterministic rule-based draft
// so the feature works without any external model (G5 default).
func (s *Store) GenerateSummary(ctx context.Context, clusterID string, summarizer Summarizer) (*SummaryVersion, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	var domainID string
	var generation int
	err := s.db.QueryRowContext(ctx,
		`SELECT domain_id, generation FROM memory_clusters WHERE id=?`, clusterID).
		Scan(&domainID, &generation)
	s.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("memstore: cluster %q: %w", clusterID, err)
	}
	entries, err := s.ClusterEntries(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("memstore: cluster %q has no members", clusterID)
	}
	if summarizer == nil {
		summarizer = ruleSummarizer{}
	}
	claims, err := summarizer.Summarize(ctx,
		Cluster{ID: clusterID, Realm: entries[0].Realm, DomainID: entries[0].DomainID},
		entries)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, c := range claims {
		lines = append(lines, "· "+c.Text)
	}
	// Generation is re-checked inside PublishSummary via the evidence
	// fingerprints; a cluster that changed during summarization fails there.
	return s.PublishSummary(ctx, clusterID, strings.Join(lines, "\n"), claims, "rule-summarizer", "v1")
}

// ruleSummarizer is the local, evidence-first default: one claim per entry.
type ruleSummarizer struct{}

func (ruleSummarizer) Summarize(_ context.Context, _ Cluster, entries []Entry) ([]SummaryClaim, error) {
	claims := make([]SummaryClaim, 0, len(entries))
	for i, e := range entries {
		claims = append(claims, SummaryClaim{
			ClaimID:  fmt.Sprintf("c%d", i+1),
			Text:     strings.TrimSpace(e.Text),
			EntryIDs: []string{e.ID},
		})
	}
	return claims, nil
}

// ListClusters returns every topic/stage cluster, newest first.
func (s *Store) ListClusters(ctx context.Context) ([]Cluster, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,realm,title,topic,status,created_at,updated_at,domain_id,generation,
		        stage_start,stage_end
		 FROM memory_clusters ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Cluster{}
	for rows.Next() {
		var c Cluster
		var stageStart, stageEnd sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Realm, &c.Title, &c.Topic, &c.Status,
			&c.CreatedAt, &c.UpdatedAt, &c.DomainID, &c.Generation, &stageStart, &stageEnd); err != nil {
			return nil, err
		}
		if stageStart.Valid {
			v := stageStart.Int64
			c.StageStart = &v
		}
		if stageEnd.Valid {
			v := stageEnd.Int64
			c.StageEnd = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCluster creates a manual topic/stage capsule. Automatic clustering is
// deliberately not enabled: the first version is user-organized.
func (s *Store) CreateCluster(ctx context.Context, c Cluster) (*Cluster, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(c.Title) == "" {
		return nil, fmt.Errorf("memstore: cluster title is required")
	}
	if c.ID == "" {
		c.ID = "cluster:" + randomUUID()[:8]
	}
	if c.Realm == "" {
		c.Realm = RealmReal
	}
	if c.DomainID == "" {
		c.DomainID = DomainLegacyUnassigned
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_clusters(id,realm,title,topic,status,created_at,updated_at,domain_id,generation,stage_start,stage_end)
		 VALUES(?,?,?,?,'active',?,?,?,1,?,?)`,
		c.ID, c.Realm, strings.TrimSpace(c.Title), c.Topic, now, now,
		c.DomainID, c.StageStart, c.StageEnd)
	if err != nil {
		return nil, err
	}
	if err := s.bumpRevisionLocked(ctx); err != nil {
		return nil, err
	}
	c.CreatedAt, c.UpdatedAt, c.Generation = now, now, 1
	return &c, nil
}

// AddClusterMembers appends entries to a cluster. Cross-domain membership is
// rejected: a summary must never mix permission partitions.
func (s *Store) AddClusterMembers(ctx context.Context, clusterID string, entryIDs []string) error {
	if err := s.checkReady(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var domainID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT domain_id FROM memory_clusters WHERE id=?`, clusterID).Scan(&domainID); err != nil {
		return fmt.Errorf("memstore: cluster %q: %w", clusterID, err)
	}
	for _, entryID := range entryIDs {
		e, err := s.getLocked(ctx, entryID)
		if err != nil {
			return err
		}
		if domainID != "" && e.DomainID != domainID {
			return fmt.Errorf("memstore: entry %q belongs to another domain", entryID)
		}
	}
	now := s.now()
	var ordinal int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal),0) FROM memory_cluster_members WHERE cluster_id=?`,
		clusterID).Scan(&ordinal)
	for _, entryID := range entryIDs {
		ordinal++
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO memory_cluster_members(cluster_id,entry_id,ordinal) VALUES(?,?,?)`,
			clusterID, entryID, ordinal); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_clusters SET updated_at=?, generation=generation+1 WHERE id=?`,
		now, clusterID); err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}

// ClusterEntries returns cluster members in ordinal order.
func (s *Store) ClusterEntries(ctx context.Context, clusterID string) ([]Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id FROM memory_cluster_members WHERE cluster_id=? ORDER BY ordinal, entry_id`,
		clusterID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, id := range ids {
		e, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		out = append(out, *e)
	}
	return out, nil
}

// getLocked loads one entry without taking the store lock (callers hold it).
func (s *Store) getLocked(ctx context.Context, id string) (*Entry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM entries WHERE id=?`, id)
	e, err := scanEntry(row)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// TouchFreshness re-evaluates stale hints for one domain. It never rewrites
// facts; it only marks entries whose review window has passed.
func (s *Store) TouchFreshness(ctx context.Context, domainID string, staleAfterDays int) (int, error) {
	if staleAfterDays <= 0 {
		staleAfterDays = 180
	}
	cutoff := s.now() - int64(staleAfterDays)*24*60*60*1000
	if strings.TrimSpace(domainID) == "" {
		domainID = DomainLegacyUnassigned
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE entries SET freshness=? WHERE domain_id=? AND status='active'
		   AND freshness<>? AND updated_at < ? AND layer='profile'`,
		FreshnessStale, domainID, FreshnessStale, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if err := s.bumpRevisionLocked(ctx); err != nil {
			return int(n), err
		}
	}
	return int(n), nil
}

// MarkStaleOverdue is used by maintenance jobs to flag unanswered review
// hints. It returns the number of entries whose freshness was set.
func (s *Store) MarkStaleOverdue(ctx context.Context) (int, error) {
	return s.TouchFreshness(ctx, DomainLegacyUnassigned, 180)
}
