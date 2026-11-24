package memstore

import (
	"context"
	"fmt"
	"strings"
)

type auditContextKey struct{}

type auditInfo struct {
	actor     string
	requestID string
}

// WithAudit binds trusted host-derived audit metadata to one governed store
// call. Model output never supplies these values directly.
func WithAudit(ctx context.Context, actor, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	actor = strings.TrimSpace(actor)
	requestID = strings.TrimSpace(requestID)
	if actor == "" && requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, auditContextKey{}, auditInfo{actor: actor, requestID: requestID})
}

func auditFromContext(ctx context.Context) auditInfo {
	if ctx == nil {
		return auditInfo{}
	}
	info, _ := ctx.Value(auditContextKey{}).(auditInfo)
	return info
}

func (s *Store) installationActor(purpose string) string {
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		purpose = "system"
	}
	id := "local"
	if s != nil && s.ledger != nil && strings.TrimSpace(s.ledger.UUID()) != "" {
		id = s.ledger.UUID()
	}
	return fmt.Sprintf("%s@%s", purpose, id)
}

// auditRequest returns the caller-supplied request id or a fresh id for this
// governed operation. The full UUID is used as the cross-table idempotency key.
func auditRequest(ctx context.Context) string {
	if id := auditFromContext(ctx).requestID; id != "" {
		return id
	}
	return randomUUID()
}
