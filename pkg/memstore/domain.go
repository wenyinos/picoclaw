package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Trusted chat kinds. The host derives these from the adapter, never from
// model output.
const (
	ChatKindPrivate = "private"
	ChatKindGroup   = "group"
	ChatKindPublic  = "public"
	ChatKindAdmin   = "admin"
)

// Processing purposes bound by memory_processing_grants.
const (
	PurposeChat        = "chat"
	PurposeToolForward = "tool_forward"
	PurposeSummary     = "summary"
	PurposeExtraction  = "llm_extraction"
	PurposeEmbedding   = "embedding"
	PurposeProactive   = "proactive"
)

// MemoryContext is the trusted caller context for every memory read or write.
// The host builds it from the real channel and session; model parameters can
// never widen it.
type MemoryContext struct {
	DomainID         string `json:"domain_id"`
	OwnerPrincipalID string `json:"owner_principal_id,omitempty"`
	ActorID          string `json:"actor_id,omitempty"` // canonical platform:id
	Channel          string `json:"channel,omitempty"`
	ChatKind         string `json:"chat_kind,omitempty"`
	SessionKey       string `json:"session_key,omitempty"`
	Purpose          string `json:"purpose,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	Now              int64  `json:"now,omitempty"`
}

// LegacyContext is the conservative context for maintenance code paths that
// predate ownership (CLI repair, migration tests). It reads only the isolated
// legacy domain, so an empty context can never behave like an administrator.
func LegacyContext() MemoryContext {
	return MemoryContext{
		DomainID: DomainLegacyUnassigned,
		ChatKind: ChatKindAdmin,
		Purpose:  "maintenance",
	}
}

// Normalize fills safe defaults. An empty domain never means "all domains".
func (mc MemoryContext) Normalize() MemoryContext {
	mc.DomainID = strings.TrimSpace(mc.DomainID)
	if mc.DomainID == "" {
		mc.DomainID = DomainLegacyUnassigned
	}
	if mc.Now == 0 {
		mc.Now = time.Now().UnixMilli()
	}
	return mc
}

// AllowsPrivate reports whether this context may read audience=private rows.
// Group and public chats are restricted to shared memory even on a private
// channel: "QQ is private" must not expose group members to personal memory.
func (mc MemoryContext) AllowsPrivate() bool {
	switch mc.ChatKind {
	case ChatKindPrivate, ChatKindAdmin, "":
		return true // "" keeps CLI/maintenance compatibility
	default:
		return false
	}
}

// Visibility is the read filter derived from a context.
type Visibility struct {
	DomainID       string
	IncludePrivate bool
	AllowedRealms  map[string]bool
	GroupScope     string
}

// VisibilityFor derives the read filter. fictional material stays out of work
// and group contexts; real material stays out of role-play unless inject is on.
func VisibilityFor(mc MemoryContext, cfg Config) Visibility {
	mc = mc.Normalize()
	v := Visibility{
		DomainID:       mc.DomainID,
		IncludePrivate: mc.AllowsPrivate(),
		AllowedRealms:  map[string]bool{RealmReal: true, RealmUnknown: true},
	}
	switch mc.ChatKind {
	case ChatKindGroup, ChatKindPublic:
		// Shared chats only see real facts that were explicitly marked public.
		v.AllowedRealms[RealmWork] = false
	case ChatKindAdmin, "":
		v.AllowedRealms[RealmFictional] = true
		v.AllowedRealms[RealmWork] = true
	}
	if cfg.InjectFictional && mc.ChatKind != ChatKindGroup && mc.ChatKind != ChatKindPublic {
		v.AllowedRealms[RealmFictional] = true
	}
	return v
}

// EntryVisible applies the channel/audience check to one entry. It is the
// post-read guard used by tools that already filtered in SQL.
func EntryVisible(e Entry, channel string, privateChannels []string) bool {
	return audienceAllowed(e.Audience, channel, privateChannels)
}

// secretTerms are never stored, even when the user asks: they cannot be
// rotated or audited from a memory row.
var secretTerms = []string{
	"密码", "验证码", "api key", "apikey", "secret key", "私钥",
	"银行卡号", "信用卡号", "身份证号", "cvv", "pin code", "口令",
	"password", "passwd", "client_secret", "refresh_token", "access_token",
	"bearer ", "private key", "-----begin ", "ghp_", "github_pat_", "xoxb-",
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[a-z0-9_-]{20,}`),
	regexp.MustCompile(`(?i)\bakia[a-z0-9]{16}\b`),
}

// sensitiveTerms raise the protection level of a write. They never lower it:
// the classifier can only make an entry more protected.
var sensitiveTerms = []string{
	"api key", "apikey", "token",
	"身份证", "银行卡", "护照号", "住址", "家庭住址", "定位",
	"自杀", "自残", "抑郁", "确诊", "病历", "用药", "怀孕",
}

// SensitivityForText returns "secret" for credentials, "sensitive" for
// health/location material, and "normal" otherwise. Automatic classification
// only escalates; it never marks something as safe.
func SensitivityForText(text string) string {
	lower := strings.ToLower(text)
	for _, term := range secretTerms {
		if strings.Contains(lower, term) {
			return SensitivitySecret
		}
	}
	for _, pattern := range secretPatterns {
		if pattern.MatchString(lower) {
			return SensitivitySecret
		}
	}
	for _, term := range sensitiveTerms {
		if strings.Contains(lower, term) {
			return SensitivitySensitive
		}
	}
	return SensitivityNormal
}

// Domain is one permission partition: owner plus normalized scope.
type Domain struct {
	DomainID         string `json:"domain_id"`
	OwnerPrincipalID string `json:"owner_principal_id,omitempty"`
	ScopeKey         string `json:"scope_key,omitempty"`
	Status           string `json:"status"`
	PolicyEpoch      int64  `json:"policy_epoch"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// EnsureDomain creates or updates a domain. Only trusted management entry
// points may call it.
func (s *Store) EnsureDomain(ctx context.Context, d Domain) error {
	d.DomainID = strings.TrimSpace(d.DomainID)
	if d.DomainID == "" {
		return fmt.Errorf("memstore: domain id is required")
	}
	if d.Status == "" {
		d.Status = "active"
	}
	if d.PolicyEpoch <= 0 {
		d.PolicyEpoch = 1
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_domains(domain_id,owner_principal_id,scope_key,status,policy_epoch,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?)
		 ON CONFLICT(domain_id) DO UPDATE SET
		   owner_principal_id=excluded.owner_principal_id,
		   scope_key=excluded.scope_key,
		   status=excluded.status,
		   policy_epoch=memory_domains.policy_epoch+1,
		   updated_at=excluded.updated_at`,
		d.DomainID, d.OwnerPrincipalID, d.ScopeKey, d.Status, d.PolicyEpoch, now, now)
	if err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}

// GetDomain loads one domain.
func (s *Store) GetDomain(ctx context.Context, domainID string) (*Domain, error) {
	var d Domain
	err := s.db.QueryRowContext(ctx,
		`SELECT domain_id,owner_principal_id,scope_key,status,policy_epoch,created_at,updated_at
		 FROM memory_domains WHERE domain_id=?`, domainID).
		Scan(&d.DomainID, &d.OwnerPrincipalID, &d.ScopeKey, &d.Status, &d.PolicyEpoch, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memstore: domain %q not found", domainID)
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListDomains returns all domains for the dashboard.
func (s *Store) ListDomains(ctx context.Context) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT domain_id,owner_principal_id,scope_key,status,policy_epoch,created_at,updated_at
		 FROM memory_domains ORDER BY domain_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.DomainID, &d.OwnerPrincipalID, &d.ScopeKey, &d.Status,
			&d.PolicyEpoch, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActorBinding maps a canonical channel identity to an owner. Bindings are
// revoked, never silently transferred.
type ActorBinding struct {
	ID               int64  `json:"id,omitempty"`
	CanonicalActor   string `json:"canonical_actor"`
	OwnerPrincipalID string `json:"owner_principal_id"`
	ChannelScope     string `json:"channel_scope,omitempty"`
	BindingRevision  int64  `json:"binding_revision"`
	Revoked          bool   `json:"revoked"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// BindActor creates or re-activates a binding and increments its revision.
func (s *Store) BindActor(ctx context.Context, b ActorBinding) error {
	b.CanonicalActor = strings.TrimSpace(b.CanonicalActor)
	b.OwnerPrincipalID = strings.TrimSpace(b.OwnerPrincipalID)
	if b.CanonicalActor == "" || b.OwnerPrincipalID == "" {
		return fmt.Errorf("memstore: canonical actor and owner are required")
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_actor_bindings
		 (canonical_actor,owner_principal_id,channel_scope,binding_revision,revoked,created_at,updated_at)
		 VALUES(?,?,?,1,0,?,?)
		 ON CONFLICT(canonical_actor,owner_principal_id) DO UPDATE SET
		   channel_scope=excluded.channel_scope,
		   binding_revision=memory_actor_bindings.binding_revision+1,
		   revoked=0,
		   updated_at=excluded.updated_at`,
		b.CanonicalActor, b.OwnerPrincipalID, b.ChannelScope, now, now)
	if err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}

// RevokeActorBinding revokes a binding and bumps its revision so old grants
// and queued work that reference the revision become invalid.
func (s *Store) RevokeActorBinding(ctx context.Context, canonicalActor, owner string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE memory_actor_bindings
		 SET revoked=1, binding_revision=binding_revision+1, updated_at=?
		 WHERE canonical_actor=? AND owner_principal_id=? AND revoked=0`,
		time.Now().UnixMilli(), canonicalActor, owner)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if err := s.bumpRevisionLocked(ctx); err != nil {
			return 0, err
		}
	}
	return int(n), nil
}

// ListActorBindings returns bindings for one owner, or all when owner is "".
func (s *Store) ListActorBindings(ctx context.Context, owner string) ([]ActorBinding, error) {
	q := `SELECT id,canonical_actor,owner_principal_id,channel_scope,binding_revision,revoked,created_at,updated_at
	      FROM memory_actor_bindings`
	args := []any{}
	if strings.TrimSpace(owner) != "" {
		q += ` WHERE owner_principal_id=?`
		args = append(args, owner)
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActorBinding{}
	for rows.Next() {
		var b ActorBinding
		var revoked int
		if err := rows.Scan(&b.ID, &b.CanonicalActor, &b.OwnerPrincipalID, &b.ChannelScope,
			&b.BindingRevision, &revoked, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		b.Revoked = revoked == 1
		out = append(out, b)
	}
	return out, rows.Err()
}

// ResolveOwner returns the owner for a canonical actor, or "" when the actor
// is unbound. Unbound actors never fall back to an administrator view.
func (s *Store) ResolveOwner(ctx context.Context, canonicalActor string) (string, error) {
	var owner string
	err := s.db.QueryRowContext(ctx,
		`SELECT owner_principal_id FROM memory_actor_bindings
		 WHERE canonical_actor=? AND revoked=0 ORDER BY binding_revision DESC LIMIT 1`,
		canonicalActor).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
}

// ProcessingGrant authorizes one purpose toward one concrete recipient.
// "Cloud is allowed" is not a grant; the recipient reference is resolved by
// the host, never by the model.
type ProcessingGrant struct {
	ID               int64  `json:"id,omitempty"`
	OwnerPrincipalID string `json:"owner_principal_id,omitempty"`
	DomainID         string `json:"domain_id,omitempty"`
	Purpose          string `json:"purpose"`
	RecipientRef     string `json:"recipient_ref"`
	DataScope        string `json:"data_scope,omitempty"`
	PolicyRevision   int64  `json:"policy_revision"`
	Revoked          bool   `json:"revoked"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// GrantProcessing records a processing grant.
func (s *Store) GrantProcessing(ctx context.Context, g ProcessingGrant) error {
	if strings.TrimSpace(g.Purpose) == "" || strings.TrimSpace(g.RecipientRef) == "" {
		return fmt.Errorf("memstore: purpose and recipient_ref are required")
	}
	if g.DataScope == "" {
		g.DataScope = "long_term"
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_processing_grants
		 (owner_principal_id,domain_id,purpose,recipient_ref,data_scope,policy_revision,revoked,created_at,updated_at)
		 VALUES(?,?,?,?,?,1,0,?,?)
		 ON CONFLICT(owner_principal_id,domain_id,purpose,recipient_ref) DO UPDATE SET
		   data_scope=excluded.data_scope,
		   policy_revision=memory_processing_grants.policy_revision+1,
		   revoked=0,
		   updated_at=excluded.updated_at`,
		g.OwnerPrincipalID, g.DomainID, g.Purpose, g.RecipientRef, g.DataScope, now, now)
	if err != nil {
		return err
	}
	return s.bumpRevisionLocked(ctx)
}

// RevokeProcessing revokes grants for one purpose. An empty recipient revokes
// every recipient for that purpose.
func (s *Store) RevokeProcessing(ctx context.Context, owner, domain, purpose, recipient string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := `UPDATE memory_processing_grants SET revoked=1, policy_revision=policy_revision+1, updated_at=?
	      WHERE revoked=0 AND owner_principal_id=? AND purpose=?`
	args := []any{time.Now().UnixMilli(), owner, purpose}
	if domain != "" {
		q += ` AND domain_id=?`
		args = append(args, domain)
	}
	if recipient != "" {
		q += ` AND recipient_ref=?`
		args = append(args, recipient)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if err := s.bumpRevisionLocked(ctx); err != nil {
			return 0, err
		}
	}
	return int(n), nil
}

// ListProcessingGrants returns grants for the dashboard.
func (s *Store) ListProcessingGrants(ctx context.Context, owner string) ([]ProcessingGrant, error) {
	q := `SELECT id,owner_principal_id,domain_id,purpose,recipient_ref,data_scope,policy_revision,revoked,created_at,updated_at
	      FROM memory_processing_grants`
	args := []any{}
	if strings.TrimSpace(owner) != "" {
		q += ` WHERE owner_principal_id=?`
		args = append(args, owner)
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProcessingGrant{}
	for rows.Next() {
		var g ProcessingGrant
		var revoked int
		if err := rows.Scan(&g.ID, &g.OwnerPrincipalID, &g.DomainID, &g.Purpose, &g.RecipientRef,
			&g.DataScope, &g.PolicyRevision, &revoked, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.Revoked = revoked == 1
		out = append(out, g)
	}
	return out, rows.Err()
}

// HasProcessingGrant reports whether a live grant exists for purpose+recipient.
// Memory egress must call it at the actual send point, not only at turn start.
func (s *Store) HasProcessingGrant(ctx context.Context, owner, domain, purpose, recipient string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM memory_processing_grants
		 WHERE revoked=0 AND owner_principal_id=? AND purpose=? AND recipient_ref=?
		   AND (domain_id='' OR domain_id=?) LIMIT 1`,
		owner, purpose, recipient, domain).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// bumpRevisionLocked increments meta revision without holding a transaction.
func (s *Store) bumpRevisionLocked(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE meta SET value = CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key='revision'`); err != nil {
		return err
	}
	s.revision.Add(1)
	return nil
}
