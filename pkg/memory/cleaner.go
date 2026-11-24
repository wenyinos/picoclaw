package memory

import (
	"context"
	"encoding/json"
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
	mutator, ok := c.store.(HistoryMutator)
	if !ok {
		out.Unavailable = len(locators)
		out.Details = append(out.Details,
			"session backend does not provide an atomic history mutation")
		return out, nil
	}

	// Group by session so each file is read, verified and rewritten once.
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
		matched := make([]bool, len(locs))
		removed := 0
		err := mutator.MutateHistory(ctx, sessionKey, func(history []providers.Message) ([]providers.Message, error) {
			keep := make([]providers.Message, 0, len(history))
			for _, msg := range history {
				remove := false
				for i, loc := range locs {
					if c.matches(msg, loc) {
						matched[i] = true
						remove = true
					}
				}
				if remove {
					removed++
					continue
				}
				keep = append(keep, msg)
			}
			return keep, nil
		})
		if err != nil {
			out.Unavailable += len(locs)
			out.Details = append(out.Details,
				fmt.Sprintf("session %q: atomic rewrite failed: %v", sessionKey, err))
			continue
		}
		for i, loc := range locs {
			if !matched[i] {
				out.Unavailable++
				out.Details = append(out.Details,
					fmt.Sprintf("session %q: root %s no longer matches (unverifiable)",
						sessionKey, rootLabel(loc)))
			}
		}
		if removed > 0 {
			out.Removed += removed
			out.Sessions++
		}
	}
	return out, nil
}

func rootLabel(loc memstore.SourceLocator) string {
	if loc.RootType != "" {
		return loc.RootType
	}
	return "legacy-content"
}

func (c *LocatorCleaner) matches(msg providers.Message, loc memstore.SourceLocator) bool {
	want := loc.EvidenceHMAC
	if want == "" {
		return false
	}
	switch loc.RootType {
	case "jsonl.reasoning":
		return msg.ReasoningContent != "" && c.digest(msg.ReasoningContent) == want
	case "jsonl.tool_args":
		for _, call := range msg.ToolCalls {
			if call.Function != nil && call.Function.Arguments != "" &&
				c.digest(call.Function.Arguments) == want {
				return true
			}
		}
		return false
	case "jsonl.tool_text":
		for _, call := range msg.ToolCalls {
			if call.Function == nil || call.Function.Arguments == "" {
				continue
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
				continue
			}
			for _, value := range args {
				if text, ok := value.(string); ok && text != "" && c.digest(text) == want {
					return true
				}
			}
		}
		return false
	case "jsonl.tool_result":
		return msg.Content != "" && c.digest(msg.Content) == want
	default:
		// Legacy locators were built from the whole evidence text. Match both
		// complete message fields; never compare a fragment with a longer field.
		if msg.Content != "" && c.digest(msg.Content) == want {
			return true
		}
		if msg.ReasoningContent != "" && c.digest(msg.ReasoningContent) == want {
			return true
		}
		for _, call := range msg.ToolCalls {
			if call.Function != nil && call.Function.Arguments != "" &&
				c.digest(call.Function.Arguments) == want {
				return true
			}
		}
		return false
	}
}
