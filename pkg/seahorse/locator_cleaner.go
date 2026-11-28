package seahorse

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/memstore"
)

// LocatorCleaner removes Seahorse messages whose complete content, reasoning
// or structured part matches the versioned digest recorded at forget time.
type LocatorCleaner struct {
	engine *Engine
	digest func(string) string
}

func NewLocatorCleaner(engine *Engine, digest func(string) string) *LocatorCleaner {
	return &LocatorCleaner{engine: engine, digest: digest}
}

func (c *LocatorCleaner) CleanLocators(ctx context.Context, locators []memstore.SourceLocator) (memstore.CleanupOutcome, error) {
	out := memstore.CleanupOutcome{Backend: "seahorse"}
	if c == nil || c.engine == nil || c.digest == nil {
		return out, fmt.Errorf("seahorse: cleaner is not configured")
	}
	bySession := map[string][]memstore.SourceLocator{}
	for _, loc := range locators {
		if !strings.HasPrefix(loc.RootType, "seahorse.") {
			continue // another backend owns this locator
		}
		if strings.TrimSpace(loc.SessionKey) == "" || loc.EvidenceHMAC == "" {
			out.Unavailable++
			out.Details = append(out.Details, "seahorse: locator lacks session/root digest")
			out.Receipts = append(out.Receipts, memstore.LocatorReceipt{
				Backend: "seahorse", LocatorKey: memstore.LocatorReceiptKey(loc),
				State: memstore.ReceiptUnavailable, ReasonCode: "locator_incomplete",
			})
			continue
		}
		bySession[loc.SessionKey] = append(bySession[loc.SessionKey], loc)
	}

	for sessionKey, locs := range bySession {
		conv, err := c.engine.store.GetConversationBySessionKey(ctx, sessionKey)
		if err != nil {
			out.Unavailable += len(locs)
			out.Details = append(out.Details, fmt.Sprintf("seahorse session %q: conversation lookup failed", sessionKey))
			for _, loc := range locs {
				out.Receipts = append(out.Receipts, memstore.LocatorReceipt{
					Backend: "seahorse", LocatorKey: memstore.LocatorReceiptKey(loc),
					State: memstore.ReceiptUnavailable, ReasonCode: "conversation_lookup_failed",
				})
			}
			continue
		}
		if conv == nil {
			// Another Seahorse engine may own this session; leave receipt absent.
			continue
		}
		remove := map[int64]bool{}
		matched := make([]bool, len(locs))
		rows, err := c.engine.store.db.QueryContext(ctx,
			`SELECT message_id, content, reasoning_content FROM messages WHERE conversation_id=?`,
			conv.ConversationID)
		if err != nil {
			return out, err
		}
		type msgRow struct {
			id        int64
			content   string
			reasoning string
		}
		var messages []msgRow
		for rows.Next() {
			var m msgRow
			if err := rows.Scan(&m.id, &m.content, &m.reasoning); err != nil {
				_ = rows.Close()
				return out, err
			}
			messages = append(messages, m)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return out, err
		}
		for _, m := range messages {
			for i, loc := range locs {
				switch loc.RootType {
				case "seahorse.text":
					if m.content != "" && c.digest(m.content) == loc.EvidenceHMAC {
						remove[m.id] = true
						matched[i] = true
					}
				case "seahorse.reasoning":
					if m.reasoning != "" && c.digest(m.reasoning) == loc.EvidenceHMAC {
						remove[m.id] = true
						matched[i] = true
					}
				case "seahorse.tool_use_args", "seahorse.tool_result":
					parts, err := c.engine.store.getMessageParts(ctx, m.id)
					if err != nil {
						continue
					}
					for _, part := range parts {
						text := part.Text
						if loc.RootType == "seahorse.tool_use_args" && part.Type == "tool_use" {
							text = part.Arguments
						} else if loc.RootType == "seahorse.tool_result" && part.Type != "tool_result" {
							continue
						}
						if text != "" && c.digest(text) == loc.EvidenceHMAC {
							remove[m.id] = true
							matched[i] = true
						}
					}
				}
			}
		}
		for i, loc := range locs {
			if !matched[i] {
				out.Unavailable++
				out.Details = append(out.Details,
					fmt.Sprintf("seahorse session %q: %s no longer matches", sessionKey, loc.RootType))
				out.Receipts = append(out.Receipts, memstore.LocatorReceipt{
					Backend: "seahorse", LocatorKey: memstore.LocatorReceiptKey(loc),
					State: memstore.ReceiptUnavailable, ReasonCode: "root_no_match",
				})
			}
		}
		if len(remove) == 0 {
			continue
		}
		ids := make([]int64, 0, len(remove))
		for id := range remove {
			ids = append(ids, id)
		}
		removed, err := c.engine.DeleteMessagesBySession(ctx, sessionKey, ids)
		if err != nil {
			out.Unavailable += len(locs)
			out.Details = append(out.Details, fmt.Sprintf("seahorse session %q: delete failed: %v", sessionKey, err))
			for _, loc := range locs {
				out.Receipts = append(out.Receipts, memstore.LocatorReceipt{
					Backend: "seahorse", LocatorKey: memstore.LocatorReceiptKey(loc),
					State: memstore.ReceiptUnavailable, ReasonCode: "delete_failed",
				})
			}
			continue
		}
		for i, loc := range locs {
			if matched[i] {
				out.Receipts = append(out.Receipts, memstore.LocatorReceipt{
					Backend: "seahorse", LocatorKey: memstore.LocatorReceiptKey(loc),
					State: memstore.ReceiptRemoved, ReasonCode: "exact_root_match", Removed: 1,
				})
			}
		}
		out.Removed += removed
		out.Sessions++
	}
	return out, nil
}

func (s *Store) getMessageParts(ctx context.Context, messageID int64) ([]MessagePart, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT type, text, name, arguments, tool_call_id, media_uri, mime_type
		 FROM message_parts WHERE message_id=? ORDER BY ordinal`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessagePart
	for rows.Next() {
		var p MessagePart
		if err := rows.Scan(&p.Type, &p.Text, &p.Name, &p.Arguments, &p.ToolCallID, &p.MediaURI, &p.MimeType); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
