//go:build !mipsle && !netbsd && !(freebsd && arm)

package agent

import (
	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

func (al *AgentLoop) seahorseCleanersForCleanup(digest func(string) string) []memstore.MessageCleaner {
	if al == nil {
		return nil
	}
	al.mu.RLock()
	cm := al.contextManager
	al.mu.RUnlock()
	mgr, ok := cm.(*seahorseContextManager)
	if !ok || mgr == nil || mgr.engine == nil {
		return nil
	}
	return []memstore.MessageCleaner{seahorse.NewLocatorCleaner(mgr.engine, digest)}
}
