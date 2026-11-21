package memory

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// LocatorCleaner removes messages from JSONL sessions when their content
// exactly matches the versioned HMAC recorded at forget time. It never
// guesses: a message whose digest does not match is reported as unavailable,
// not deleted.
//
// The digest function must be the forget ledger's Digest, so the comparison
// uses the same key and normalization the locator was created with.
type LocatorCleaner struct {
	store  Store
	digest func(string) string
}

// NewLocatorCleaner builds a cleaner over a session store.
func NewLocatorCleaner(store Store, digest func(string) string) *LocatorCleaner {
	return &LocatorCleaner{store: store, digest: digest}
}

// CleanLocators implements memstore.MessageCleaner.
func (c *LocatorCleaner) CleanLocators(
	ctx context.Context,
	locators []memstore.SourceLocator,
) (memstore.CleanupOutcome, error) {
	out := memstore.CleanupOutcome{}
	if c == nil || c.store == nil || c.digest == nil {
		return out, fmt.Errorf("memory: cleaner is not configured")
	}
	// Group by session so each file is read and written once.
	bySession := map[string][]memstore.SourceLocator{}
	for _, loc := range locators {
		if loc.SessionKey == "" || loc.EvidenceHMAC == "" {
			out.Unavailable++
			out.Details = append(out.Details,
				fmt.Sprintf("session %q: no exact content locator", loc.SessionKey))
			continue
		}
		bySession[loc.SessionKey] = append(bySession[loc.SessionKey], loc)
	}

	for sessionKey, locs := range bySession {
		history, err := c.store.GetHistory(ctx, sessionKey)
		if err != nil {
			out.Unavailable += len(locs)
			out.Details = append(out.Details,
				fmt.Sprintf("session %q: %v", sessionKey, err))
			continue
		}
		remove := make([]bool, len(history))
		matched := map[string]bool{}
		for i, msg := range history {
			for _, loc := range locs {
				if loc.EvidenceHMAC == "" || matched[loc.EvidenceHMAC] {
					continue
				}
				if c.matches(msg, loc.EvidenceHMAC) {
					remove[i] = true
					matched[loc.EvidenceHMAC] = true
					break
				}
			}
		}
		for _, loc := range locs {
			if !matched[loc.EvidenceHMAC] {
				out.Unavailable++
				out.Details = append(out.Details,
					fmt.Sprintf("session %q: message content no longer matches (unverifiable)", sessionKey))
			}
		}
		kept := make([]providers.Message, 0, len(history))
		removed := 0
		for i, msg := range history {
			if remove[i] {
				removed++
				continue
			}
			kept = append(kept, msg)
		}
		if removed == 0 {
			continue
		}
		if err := c.store.SetHistory(ctx, sessionKey, kept); err != nil {
			out.Unavailable += len(locs)
			out.Details = append(out.Details,
				fmt.Sprintf("session %q: rewrite failed: %v", sessionKey, err))
			continue
		}
		_ = c.store.Compact(ctx, sessionKey)
		out.Removed += removed
		out.Sessions++
	}
	return out, nil
}

func (c *LocatorCleaner) matches(msg providers.Message, want string) bool {
	if msg.Content != "" {
		if d := c.digest(msg.Content); d == want {
			return true
		}
	}
	if msg.ReasoningContent != "" {
		if d := c.digest(msg.ReasoningContent); d == want {
			return true
		}
	}
	return false
}
