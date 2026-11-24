package seahorse

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memstore"
)

func seahorseTestDigest(text string) string { return "d:" + text }

func TestSeahorseLocatorCleanerDeletesMessagesAndSummaries(t *testing.T) {
	engine := newTestEngine(t)
	defer engine.Close()
	ctx := context.Background()
	sessionKey := "agent:owner"
	target := "seahorse forgotten content"
	if _, err := engine.Ingest(ctx, sessionKey, []Message{
		{Role: "user", Content: target, TokenCount: 2},
		{Role: "user", Content: "surviving message", TokenCount: 2},
	}); err != nil {
		t.Fatal(err)
	}
	conv, err := engine.store.GetConversationBySessionKey(ctx, sessionKey)
	if err != nil || conv == nil {
		t.Fatalf("conversation: %+v %v", conv, err)
	}
	messages, err := engine.store.GetMessages(ctx, conv.ConversationID, 10, 0)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages: %+v %v", messages, err)
	}
	targetID := messages[0].ID
	if _, err := engine.store.db.ExecContext(ctx,
		`INSERT INTO summaries(summary_id,conversation_id,kind,depth,content,token_count)
		 VALUES('summary-target',?, 'leaf',0,'derived text',5)`, conv.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.store.db.ExecContext(ctx,
		`INSERT INTO summary_messages(summary_id,message_id,ordinal) VALUES('summary-target',?,0)`,
		targetID); err != nil {
		t.Fatal(err)
	}

	cleaner := NewLocatorCleaner(engine, seahorseTestDigest)
	outcome, err := cleaner.CleanLocators(ctx, []memstore.SourceLocator{{
		SessionKey: sessionKey, RootType: "seahorse.text",
		EvidenceHMAC: seahorseTestDigest(target),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Removed != 1 || outcome.Unavailable != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	remaining, err := engine.store.GetMessages(ctx, conv.ConversationID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Content != "surviving message" {
		t.Fatalf("remaining messages = %+v", remaining)
	}
	var summaries int
	if err := engine.store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM summaries WHERE summary_id='summary-target'`).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 0 {
		t.Fatal("summary derived from forgotten message survived")
	}
}
