package memstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GetRelationship returns current (valid_to IS NULL) relationship attributes.
func (s *Store) GetRelationship(ctx context.Context) ([]RelationshipState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT key,value,valid_from,confidence FROM relationship_state
		 WHERE valid_to IS NULL ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelationshipState
	for rows.Next() {
		var r RelationshipState
		if err := rows.Scan(&r.Key, &r.Value, &r.ValidFrom, &r.Confidence); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetRelationship updates one relationship attribute and closes its previous version.
func (s *Store) SetRelationship(ctx context.Context, key, value string, confidence float64) error {
	key = trimSpace(key)
	if key == "" {
		return fmt.Errorf("memstore: empty relationship key")
	}
	if confidence <= 0 || confidence > 1 {
		confidence = 0.8
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE relationship_state SET valid_to=? WHERE key=? AND valid_to IS NULL`, now, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO relationship_state(key,value,valid_from,valid_to,confidence,updated_at)
		 VALUES(?,?,?,NULL,?,?)
		 ON CONFLICT(key) DO UPDATE SET
		   value=excluded.value, valid_from=excluded.valid_from, valid_to=NULL,
		   confidence=excluded.confidence, updated_at=excluded.updated_at`,
		key, trimSpace(value), now, confidence, now); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ListPersonas returns all persona versions.
func (s *Store) ListPersonas(ctx context.Context) ([]Persona, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,version,content,active,created_at FROM personas ORDER BY active DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		var p Persona
		var active int
		if err := rows.Scan(&p.ID, &p.Name, &p.Version, &p.Content, &active, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Active = active != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetActivePersona returns the active persona, or nil when none exists.
func (s *Store) GetActivePersona(ctx context.Context) (*Persona, error) {
	var p Persona
	var active int
	err := s.db.QueryRowContext(ctx,
		`SELECT id,name,version,content,active,created_at FROM personas WHERE active=1 LIMIT 1`).
		Scan(&p.ID, &p.Name, &p.Version, &p.Content, &active, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Active = active != 0
	return &p, nil
}

// UpsertPersona creates a new persona version and optionally activates it.
func (s *Store) UpsertPersona(ctx context.Context, id, name, content string, activate bool) (*Persona, error) {
	if trimSpace(content) == "" {
		return nil, fmt.Errorf("memstore: empty persona content")
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
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM personas WHERE id=?`, id).Scan(&version)
	if version == 0 {
		version = 1
	}
	if activate {
		if _, err := tx.ExecContext(ctx, `UPDATE personas SET active=0`); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO personas(id,name,version,content,active,created_at) VALUES(?,?,?,?,?,?)`,
		id, trimSpace(name), version, trimSpace(content), boolInt(activate), now); err != nil {
		return nil, err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Persona{ID: id, Name: name, Version: version, Content: content, Active: activate, CreatedAt: now}, nil
}

// ActivatePersona switches the active persona.
func (s *Store) ActivatePersona(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE personas SET active=0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE personas SET active=1 WHERE id=?`, id); err != nil {
		return err
	}
	if err := s.bumpRevision(ctx, tx); err != nil {
		return err
	}
	_ = now
	return tx.Commit()
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
