package memstore

import (
	"context"
	"time"
)

// RecordUsage buffers injected memory IDs and flushes them in batches. It is
// intentionally non-blocking: prompt assembly must not wait for SQLite writes.
func (s *Store) RecordUsage(ids []string) {
	if s == nil || len(ids) == 0 {
		return
	}
	s.usageMu.Lock()
	if s.usage == nil {
		s.usage = make(map[string]int)
	}
	for _, id := range ids {
		if id != "" {
			s.usage[id]++
		}
	}
	shouldFlush := len(s.usage) >= 100 || time.Since(s.usageLastFlush) >= 5*time.Second
	startFlush := shouldFlush && !s.usageFlushing
	if startFlush {
		s.usageFlushing = true
	}
	s.usageMu.Unlock()

	if startFlush {
		s.usageWG.Add(1)
		go func() {
			defer s.usageWG.Done()
			s.flushUsage(context.Background())
			s.usageMu.Lock()
			s.usageFlushing = false
			s.usageMu.Unlock()
		}()
	}
}

func (s *Store) flushUsage(ctx context.Context) {
	s.usageMu.Lock()
	if len(s.usage) == 0 {
		s.usageLastFlush = time.Now()
		s.usageMu.Unlock()
		return
	}
	pending := s.usage
	s.usage = make(map[string]int)
	s.usageLastFlush = time.Now()
	s.usageMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UnixMilli()
	for id, count := range pending {
		if _, err := tx.ExecContext(ctx,
			`UPDATE entries SET use_count=use_count+?, last_used_at=? WHERE id=?`,
			count, now, id); err != nil {
			return
		}
	}
	_ = tx.Commit()
}
