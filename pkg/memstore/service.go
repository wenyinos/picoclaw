package memstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrOwnerApprovalRequired is returned when chat-originated model actions try
// to perform owner-only governance work.
var ErrOwnerApprovalRequired = errors.New("memstore: owner approval is required for this action")

const maxRelationshipValueRunes = 256

// MemoryService is the single governed write/read entry point. Every channel,
// tool, CLI command and dashboard handler should go through it instead of
// calling Store methods directly; Store remains the repository layer.
type MemoryService struct {
	store *Store
	cfg   Config
}

// NewMemoryService wraps a store with policy enforcement.
func NewMemoryService(store *Store, cfg Config) *MemoryService {
	return &MemoryService{store: store, cfg: cfg}
}

// Store exposes the repository for maintenance paths that need raw access.
// Normal request handling must not call it to bypass policy.
func (svc *MemoryService) Store() *Store { return svc.store }

// authorizeRead rejects contexts that carry no identity at all for sensitive
// material. Ordinary reads are constrained by domain/audience in SQL.
func (svc *MemoryService) authorizeRead(mc MemoryContext) (MemoryContext, error) {
	mc = mc.Normalize()
	if strings.TrimSpace(mc.DomainID) == "" {
		return mc, fmt.Errorf("memstore: no domain binding for this request")
	}
	return mc, nil
}

// authorizeWrite blocks personal long-term writes from shared chats and
// secrets from every chat (FR-12).
func (svc *MemoryService) authorizeWrite(mc MemoryContext, sensitivity string) (MemoryContext, error) {
	mc = mc.Normalize()
	if mc.ChatKind == ChatKindGroup || mc.ChatKind == ChatKindPublic {
		return mc, fmt.Errorf("memstore: shared chats cannot write personal long-term memory")
	}
	switch sensitivity {
	case SensitivitySecret:
		return mc, fmt.Errorf("memstore: credentials and secrets are never stored in long-term memory")
	case SensitivitySensitive:
		if mc.ChatKind != ChatKindPrivate && mc.ChatKind != ChatKindAdmin {
			return mc, fmt.Errorf("memstore: sensitive memory needs a private chat or explicit approval")
		}
	}
	return mc, nil
}

// requireOwner rejects chat-originated model actions that change approval,
// lifecycle, trust, relationship or core-rule state. The model may only use
// the controlled actions allowlisted by the memory tool.
func (svc *MemoryService) auditContext(ctx context.Context, mc MemoryContext) context.Context {
	mc = mc.Normalize()
	actor := strings.TrimSpace(mc.ActorID)
	if actor == "" {
		actor = svc.store.installationActor(mc.Purpose)
	}
	return WithAudit(ctx, actor, "")
}

func requireOwner(mc MemoryContext) error {
	if mc.Normalize().Purpose == PurposeChat && mc.ChatKind != ChatKindAdmin {
		return ErrOwnerApprovalRequired
	}
	return nil
}

// Add applies capture policy and writes through the store.
func (svc *MemoryService) Add(ctx context.Context, mc MemoryContext, in AddInput) (*AddResult, error) {
	mc = mc.Normalize()
	if mc.Purpose == PurposeChat && normalizeLayer(in.Layer) == LayerBoundary {
		return &AddResult{Action: "blocked_policy"}, ErrOwnerApprovalRequired
	}
	in.DomainID = mc.DomainID
	if strings.TrimSpace(in.Sensitivity) == "" {
		in.Sensitivity = SensitivityForText(in.Text)
	}
	mc, err := svc.authorizeWrite(mc, in.Sensitivity)
	if err != nil {
		return &AddResult{Action: "blocked_policy"}, err
	}
	return svc.store.Add(ctx, in)
}

// Search applies the visibility predicate.
func (svc *MemoryService) Search(ctx context.Context, mc MemoryContext, opts SearchOptions) ([]Entry, error) {
	mc, err := svc.authorizeRead(mc)
	if err != nil {
		return nil, err
	}
	opts.DomainID = mc.DomainID
	opts.Channel = firstNonEmpty(opts.Channel, mc.Channel)
	opts.ChatKindHint = mc.ChatKind
	opts.ExcludeExpired = true
	if len(opts.PrivateChannelExclusions) == 0 {
		opts.PrivateChannelExclusions = svc.cfg.PrivateChannelExclusions
	}
	if mc.ChatKind == ChatKindGroup || mc.ChatKind == ChatKindPublic {
		opts.IncludeFic = false
	}
	return svc.store.Search(ctx, opts)
}

// Get returns one entry after the visibility check.
func (svc *MemoryService) Get(ctx context.Context, mc MemoryContext, id string) (*Entry, error) {
	mc, err := svc.authorizeRead(mc)
	if err != nil {
		return nil, err
	}
	e, err := svc.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.DomainID != "" && e.DomainID != mc.DomainID {
		return nil, fmt.Errorf("memstore: memory not found")
	}
	if !EntryVisible(*e, mc.Channel, svc.cfg.PrivateChannelExclusions) {
		return nil, fmt.Errorf("memstore: memory not found")
	}
	if !audienceAllowedForChat(e.Audience, mc.ChatKind, mc.Channel, svc.cfg.PrivateChannelExclusions) {
		return nil, fmt.Errorf("memstore: memory not found")
	}
	return e, nil
}

// Confirm records a user confirmation event.
func (svc *MemoryService) Confirm(ctx context.Context, mc MemoryContext, id, evidence string) error {
	if err := requireOwner(mc); err != nil {
		return err
	}
	mc, err := svc.authorizeWrite(mc, "")
	if err != nil {
		return err
	}
	if err := svc.requireTarget(ctx, mc, id); err != nil {
		return err
	}
	return svc.store.Confirm(svc.auditContext(ctx, mc), id, evidence)
}

// Correct creates a new version.
func (svc *MemoryService) Correct(ctx context.Context, mc MemoryContext, id string, in AddInput) (*AddResult, error) {
	mc, err := svc.authorizeWrite(mc, "")
	if err != nil {
		return nil, err
	}
	old, err := svc.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if old.DomainID != "" && old.DomainID != mc.DomainID {
		return nil, fmt.Errorf("memstore: memory not found")
	}
	if mc.Purpose == PurposeChat {
		if in.Force || old.PinnedCore {
			return nil, ErrOwnerApprovalRequired
		}
		if old.Layer == LayerBoundary || normalizeLayer(in.Layer) == LayerBoundary {
			return nil, ErrOwnerApprovalRequired
		}
		if in.Layer != "" && normalizeLayer(in.Layer) != old.Layer {
			return nil, fmt.Errorf("memstore: chat-originated corrections cannot change layer")
		}
		if in.Audience != "" && in.Audience != old.Audience {
			return nil, fmt.Errorf("memstore: chat-originated corrections cannot change audience")
		}
		if in.Scope != "" && in.Scope != old.Scope {
			return nil, fmt.Errorf("memstore: chat-originated corrections cannot change scope")
		}
	}
	in.DomainID = mc.DomainID
	return svc.store.Correct(svc.auditContext(ctx, mc), id, in)
}

// UpdateMetadata applies non-content, non-status metadata through the governed
// write path. Text changes must use Correct.
func (svc *MemoryService) UpdateMetadata(ctx context.Context, mc MemoryContext, id string, in AddInput) (*Entry, error) {
	mc, err := svc.authorizeWrite(mc, "")
	if err != nil {
		return nil, err
	}
	if err := svc.requireTarget(ctx, mc, id); err != nil {
		return nil, err
	}
	in.DomainID = mc.DomainID
	in.Force = false
	in.ForceStatus = ""
	in.PinnedCore = false
	return svc.store.Update(svc.auditContext(ctx, mc), id, in)
}

// SetStatus changes a memory's lifecycle status through the governed write
// path. Shared chats cannot archive or restore personal memories.
func (svc *MemoryService) SetStatus(ctx context.Context, mc MemoryContext, id, status string) error {
	if err := requireOwner(mc); err != nil {
		return err
	}
	mc, err := svc.authorizeWrite(mc, "")
	if err != nil {
		return err
	}
	if err := svc.requireTarget(ctx, mc, id); err != nil {
		return err
	}
	return svc.store.SetStatus(svc.auditContext(ctx, mc), id, status)
}

// SetRelationshipState updates one derived relationship attribute. It is a
// write: group/public chats are rejected before the store is touched.
func (svc *MemoryService) SetRelationshipState(ctx context.Context, mc MemoryContext, update RelationshipUpdate) error {
	if err := requireOwner(mc); err != nil {
		return err
	}
	if utf8.RuneCountInString(update.Value) > maxRelationshipValueRunes {
		return fmt.Errorf("memstore: relationship value exceeds %d characters", maxRelationshipValueRunes)
	}
	sensitivity := SensitivityForText(update.Value)
	mc, err := svc.authorizeWrite(mc, sensitivity)
	if err != nil {
		return err
	}
	if strings.TrimSpace(update.DomainID) == "" {
		update.DomainID = mc.DomainID
	}
	return svc.store.SetRelationshipFull(svc.auditContext(ctx, mc), update)
}

// Forget executes or previews a permanent erase. Owner approval is the
// caller's responsibility (dashboard auth or CLI maintenance mode).
func (svc *MemoryService) Forget(ctx context.Context, mc MemoryContext, id string, dryRun bool) (*ForgetPreview, error) {
	mc, err := svc.authorizeWrite(mc, "")
	if err != nil {
		return nil, err
	}
	if err := svc.requireTarget(ctx, mc, id); err != nil {
		return nil, err
	}
	return svc.store.Forget(svc.auditContext(ctx, mc), id, dryRun)
}

func (svc *MemoryService) requireTarget(ctx context.Context, mc MemoryContext, id string) error {
	e, err := svc.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("memstore: memory not found")
	}
	if e.DomainID != "" && e.DomainID != mc.DomainID {
		return fmt.Errorf("memstore: memory not found")
	}
	return nil
}

// ChangePlan is a one-shot previewed governance action. The model may create
// a plan; only an owner-approved plan executes (FR-05).
type ChangePlan struct {
	PlanID           string `json:"plan_id"`
	Action           string `json:"action"`
	TargetKind       string `json:"target_kind"`
	TargetID         string `json:"target_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	ParamsDigest     string `json:"params_digest"`
	Payload          string `json:"payload,omitempty"`
	Status           string `json:"status"`
	ApprovedBy       string `json:"approved_by,omitempty"`
	ExpiresAt        int64  `json:"expires_at"`
	CreatedAt        int64  `json:"created_at"`
	ExecutedAt       *int64 `json:"executed_at,omitempty"`
}

// CreateChangePlan previews an action and stores it as a pending plan.
func (svc *MemoryService) CreateChangePlan(ctx context.Context, mc MemoryContext, action, targetID string, payload any) (*ChangePlan, error) {
	mc, err := svc.authorizeWrite(mc, "")
	if err != nil {
		return nil, err
	}
	switch action {
	case "correct", "confirm", "forget", "reassign_audience", "set_relationship":
	default:
		return nil, fmt.Errorf("memstore: unsupported plan action %q", action)
	}
	var expectedRevision int64
	targetKind := "entry"
	if action == "set_relationship" {
		if strings.TrimSpace(targetID) == "" {
			return nil, fmt.Errorf("memstore: relationship plans need a key")
		}
		targetKind = "relationship"
	} else {
		target, err := svc.store.Get(ctx, targetID)
		if err != nil {
			return nil, fmt.Errorf("memstore: target not found")
		}
		if target.DomainID != "" && target.DomainID != mc.DomainID {
			return nil, fmt.Errorf("memstore: target not found")
		}
		expectedRevision = target.RowRevision
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	now := svc.store.now()
	plan := &ChangePlan{
		PlanID:           "plan_" + randomUUID()[:12],
		Action:           action,
		TargetKind:       targetKind,
		TargetID:         targetID,
		ExpectedRevision: expectedRevision,
		ParamsDigest:     ContentHash(Normalize(string(raw))),
		Payload:          string(raw),
		Status:           "pending",
		ExpiresAt:        now + 10*60*1000,
		CreatedAt:        now,
	}
	svc.store.mu.Lock()
	defer svc.store.mu.Unlock()
	_, err = svc.store.db.ExecContext(ctx,
		`INSERT INTO memory_change_plans
		 (plan_id,action,target_kind,target_id,expected_revision,params_digest,payload,
		  status,expires_at,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		plan.PlanID, plan.Action, plan.TargetKind, plan.TargetID, plan.ExpectedRevision,
		plan.ParamsDigest, plan.Payload, plan.Status, plan.ExpiresAt, plan.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := svc.store.bumpRevisionLocked(ctx); err != nil {
		return nil, err
	}
	return plan, nil
}

// GetChangePlan loads one plan.
func (svc *MemoryService) GetChangePlan(ctx context.Context, planID string) (*ChangePlan, error) {
	var p ChangePlan
	var executed sql.NullInt64
	err := svc.store.db.QueryRowContext(ctx,
		`SELECT plan_id,action,target_kind,target_id,expected_revision,params_digest,payload,
		        status,approved_by,expires_at,created_at,executed_at
		 FROM memory_change_plans WHERE plan_id=?`, planID).
		Scan(&p.PlanID, &p.Action, &p.TargetKind, &p.TargetID, &p.ExpectedRevision,
			&p.ParamsDigest, &p.Payload, &p.Status, &p.ApprovedBy, &p.ExpiresAt,
			&p.CreatedAt, &executed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memstore: plan %q not found", planID)
	}
	if err != nil {
		return nil, err
	}
	if executed.Valid {
		v := executed.Int64
		p.ExecutedAt = &v
	}
	return &p, nil
}

// ApproveChangePlan executes a pending plan after re-validating the target
// revision. Stale plans return a conflict instead of overwriting newer data.
func (svc *MemoryService) ApproveChangePlan(ctx context.Context, mc MemoryContext, planID, approver string) (*ChangePlan, error) {
	mc = mc.Normalize()
	if mc.ChatKind != ChatKindAdmin {
		return nil, fmt.Errorf("memstore: plan approval requires the dashboard/admin context")
	}
	p, err := svc.GetChangePlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	now := svc.store.now()
	if p.Status != "pending" {
		return nil, fmt.Errorf("memstore: plan %q is %s, not pending", planID, p.Status)
	}
	if now > p.ExpiresAt {
		_, _ = svc.store.db.ExecContext(ctx,
			`UPDATE memory_change_plans SET status='expired' WHERE plan_id=?`, planID)
		return nil, fmt.Errorf("memstore: plan %q expired; create a new preview", planID)
	}
	if p.Action != "set_relationship" {
		target, err := svc.store.Get(ctx, p.TargetID)
		if err != nil {
			return nil, fmt.Errorf("memstore: target no longer exists")
		}
		if target.RowRevision != p.ExpectedRevision {
			return nil, fmt.Errorf("memstore: target changed since preview (revision %d != %d)",
				target.RowRevision, p.ExpectedRevision)
		}
	}

	// Claim the plan atomically before any side effect. A repeated approval
	// cannot execute the action twice, even when the final status write fails.
	res, err := svc.store.db.ExecContext(ctx,
		`UPDATE memory_change_plans SET status='executing' WHERE plan_id=? AND status='pending'`, planID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("memstore: plan %q was already claimed", planID)
	}

	switch p.Action {
	case "forget":
		if _, err := svc.store.Forget(ctx, p.TargetID, false); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
	case "confirm":
		if err := svc.store.Confirm(ctx, p.TargetID, "plan approval"); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
	case "correct":
		var in AddInput
		if err := json.Unmarshal([]byte(p.Payload), &in); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
		if _, err := svc.store.Correct(ctx, p.TargetID, in); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
	case "reassign_audience":
		var in AddInput
		if err := json.Unmarshal([]byte(p.Payload), &in); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
		target, err := svc.store.Get(ctx, p.TargetID)
		if err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
		in.Force = in.Force || target.PinnedCore
		if _, err := svc.store.Update(ctx, p.TargetID, in); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
	case "set_relationship":
		var u RelationshipUpdate
		if err := json.Unmarshal([]byte(p.Payload), &u); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
		if strings.TrimSpace(u.Key) == "" {
			u.Key = p.TargetID
		}
		u.DomainID = mc.DomainID
		if err := svc.store.SetRelationshipFull(ctx, u); err != nil {
			svc.markPlanFailed(ctx, planID, err)
			return nil, err
		}
	}
	svc.store.mu.Lock()
	_, err = svc.store.db.ExecContext(ctx,
		`UPDATE memory_change_plans SET status='executed', approved_by=?, executed_at=?,
		 payload='' WHERE plan_id=?`, approver, now, planID)
	svc.store.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return svc.GetChangePlan(ctx, planID)
}

func (svc *MemoryService) markPlanFailed(ctx context.Context, planID string, cause error) {
	_, _ = svc.store.db.ExecContext(ctx,
		`UPDATE memory_change_plans SET status='failed' WHERE plan_id=? AND status='executing'`, planID)
}

// ListChangePlans lists plans by status for the dashboard inbox.
func (svc *MemoryService) ListChangePlans(ctx context.Context, status string, limit int) ([]ChangePlan, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT plan_id,action,target_kind,target_id,expected_revision,params_digest,payload,
	             status,approved_by,expires_at,created_at,executed_at
	      FROM memory_change_plans`
	args := []any{}
	if strings.TrimSpace(status) != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := svc.store.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChangePlan{}
	for rows.Next() {
		var p ChangePlan
		var executed sql.NullInt64
		if err := rows.Scan(&p.PlanID, &p.Action, &p.TargetKind, &p.TargetID,
			&p.ExpectedRevision, &p.ParamsDigest, &p.Payload, &p.Status, &p.ApprovedBy,
			&p.ExpiresAt, &p.CreatedAt, &executed); err != nil {
			return nil, err
		}
		if executed.Valid {
			v := executed.Int64
			p.ExecutedAt = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
