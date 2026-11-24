//go:build mipsle || netbsd || (freebsd && arm)

package agent

import "github.com/sipeed/picoclaw/pkg/memstore"

// Seahorse is not built on these targets, so cleanup has no Seahorse backend.
func (al *AgentLoop) seahorseCleanersForCleanup(func(string) string) []memstore.MessageCleaner {
	return nil
}
