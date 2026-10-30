package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func scanPersona(row scanner) (Persona, error) {
	var p Persona
	var active int
	err := row.Scan(&p.ID, &p.Name, &p.Version, &p.Content, &active,
		&p.Realm, &p.Audience, &p.Scope, &p.CreatedAt)
	if err != nil {
		return Persona{}, err
	}
	p.Active = active != 0
	return p, nil
}

// GetRelationship returns current (valid_to IS NULL, not expired) attributes.
func (s *Store) GetRelationship(ctx context.Context) ([]RelationshipState, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.QueryContext(ctx,
		`SELECT key,value,valid_from,confidence,expires_at FROM relationship_state
		 WHERE valid_to IS NULL AND (expires_at IS NULL OR expires_at > ?) ORDER BY key`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelationshipState
	for rows.Next() {
		var r RelationshipState
		var expires sql.NullInt64
		if err := rows.Scan(&r.Key, &r.Value, &r.ValidFrom, &r.Confidence, &expires); err != nil {
			return nil, err
		}
		if expires.Valid {
			v := expires.Int64
			r.ExpiresAt = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRelationship updates one relationship attribute and archives its previous
// value in relationship_state_history.
func (s *Store) SetRelationship(ctx context.Context, key, value string, confidence float64) error {
	return s.SetRelationshipWithTTL(ctx, key, value, confidence, nil)
}

// SetRelationshipWithTTL is SetRelationship with an optional expiry.
func (s *Store) SetRelationshipWithTTL(
	ctx context.Context,
	key, value string,
	confidence float64,
	expiresAt *int64,
) error {
	key = trimSpace(key)
	if key == "" {
		return fmt.Errorf("memstore: empty relationship key")
	}
	if confidence <= 0 || confidence > 1 {
		confidence = 0.8
	}
	now := time.Now().UnixMilli()
	var exp any
	if expiresAt != nil {
		exp = *expiresAt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var oldValue string
	var oldValidFrom int64
	var oldConfidence float64
	var oldUpdated int64
	var oldExpires sql.NullInt64
	err = tx.QueryRowContext(ctx,
		`SELECT value,valid_from,confidence,updated_at,expires_at
		 FROM relationship_state WHERE key=? AND valid_to IS NULL`, key).
		Scan(&oldValue, &oldValidFrom, &oldConfidence, &oldUpdated, &oldExpires)
	if err == nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO relationship_state_history
			 (key,value,valid_from,valid_to,confidence,updated_at)
			 VALUES(?,?,?,?,?,?)`,
			key, oldValue, oldValidFrom, now, oldConfidence, oldUpdated); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO relationship_state(key,value,valid_from,valid_to,confidence,updated_at,expires_at)
		 VALUES(?,?,?,NULL,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET
		   value=excluded.value, valid_from=excluded.valid_from, valid_to=NULL,
		   confidence=excluded.confidence, updated_at=excluded.updated_at,
		   expires_at=excluded.expires_at`,
		key, trimSpace(value), now, confidence, now, exp); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// GetRelationshipHistory returns archived versions for one key, newest first.
func (s *Store) GetRelationshipHistory(ctx context.Context, key string) ([]RelationshipHistory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key,value,valid_from,valid_to,confidence,updated_at
		 FROM relationship_state_history WHERE key=? ORDER BY valid_from DESC LIMIT 100`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelationshipHistory
	for rows.Next() {
		var h RelationshipHistory
		if err := rows.Scan(&h.Key, &h.Value, &h.ValidFrom, &h.ValidTo, &h.Confidence, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListPersonas returns all persona versions.
func (s *Store) ListPersonas(ctx context.Context) ([]Persona, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,version,content,active,realm,audience,scope,created_at
		 FROM personas ORDER BY active DESC, created_at DESC, id, version DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetActivePersona returns the first active persona for administrative views.
func (s *Store) GetActivePersona(ctx context.Context) (*Persona, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,name,version,content,active,realm,audience,scope,created_at
		 FROM personas WHERE active=1 ORDER BY created_at DESC, version DESC LIMIT 1`)
	p, err := scanPersona(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetActivePersonaForChannel selects an active persona that is allowed in the
// current channel and fictional mode. This is the only path BuildFixedContext
// should use; the administrative GetActivePersona ignores channel filters.
func (s *Store) GetActivePersonaForChannel(
	ctx context.Context,
	channel string,
	privateChannels []string,
	injectFictional bool,
) (*Persona, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,version,content,active,realm,audience,scope,created_at
		 FROM personas WHERE active=1 ORDER BY created_at DESC, version DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		if p.Realm == RealmFictional && !injectFictional {
			continue
		}
		if !audienceAllowed(p.Audience, channel, privateChannels) {
			continue
		}
		return &p, nil
	}
	return nil, rows.Err()
}

// UpsertPersona creates a new persona version and optionally activates it.
func (s *Store) UpsertPersona(ctx context.Context, id, name, content string, activate bool) (*Persona, error) {
	return s.UpsertPersonaScoped(ctx, id, name, content, activate, RealmReal, "private", "user")
}

// UpsertPersonaScoped creates a new version with explicit realm/audience/scope.
func (s *Store) UpsertPersonaScoped(
	ctx context.Context,
	id, name, content string,
	activate bool,
	realm, audience, scope string,
) (*Persona, error) {
	if trimSpace(content) == "" {
		return nil, fmt.Errorf("memstore: empty persona content")
	}
	realm = normalizeRealm(realm)
	audience = normalizePersonaAudience(audience)
	scope = trimSpace(scope)
	if scope == "" {
		scope = "user"
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var version int
	_ = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version),0)+1 FROM personas WHERE id=?`, id).Scan(&version)
	if version == 0 {
		version = 1
	}
	if activate {
		if _, err := tx.ExecContext(ctx,
			`UPDATE personas SET active=0 WHERE scope=?`, scope); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO personas(id,name,version,content,active,realm,audience,scope,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		id, trimSpace(name), version, trimSpace(content), boolInt(activate),
		realm, audience, scope, now); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Persona{
		ID: id, Name: trimSpace(name), Version: version, Content: trimSpace(content),
		Active: activate, Realm: realm, Audience: audience, Scope: scope, CreatedAt: now,
	}, nil
}

// ActivatePersona switches to the latest version of id within its scope.
func (s *Store) ActivatePersona(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int
	var scope string
	if err := tx.QueryRowContext(ctx,
		`SELECT version,scope FROM personas WHERE id=? ORDER BY version DESC LIMIT 1`, id).
		Scan(&version, &scope); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("memstore: persona %q not found", id)
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE personas SET active=0 WHERE scope=?`, scope); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE personas SET active=1 WHERE id=? AND version=?`, id, version); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func normalizePersonaAudience(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "private"
	}
	if v == "all" || v == "private" || strings.HasPrefix(v, "channel:") {
		return v
	}
	return "private"
}

func trimSpace(v string) string {
	return strings.TrimSpace(v)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
