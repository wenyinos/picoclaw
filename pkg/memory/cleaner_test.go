package memory

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memstore"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func testDigest(text string) string {
	return "digest:" + text
}

func TestLocatorCleanerRemovesEveryVerifiedDuplicateAndToolText(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	key := "session-cleanup"
	duplicate := "这是一条很长但不允许靠截断猜测的用户原始消息"
	for i := 0; i < 2; i++ {
		if err := store.AddFullMessage(ctx, key, providers.Message{
			Role: "user", Content: duplicate,
		}); err != nil {
			t.Fatal(err)
		}
	}
	memoryText := "模型通过工具保存的正文"
	if err := store.AddFullMessage(ctx, key, providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID: "call-1",
			Function: &providers.FunctionCall{
				Name: "memory", Arguments: `{"action":"add","text":"` + memoryText + `"}`,
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddFullMessage(ctx, key, providers.Message{
		Role: "user", Content: "unrelated message",
	}); err != nil {
		t.Fatal(err)
	}

	cleaner := NewLocatorCleaner(store, testDigest)
	outcome, err := cleaner.CleanLocators(ctx, []memstore.SourceLocator{
		{SessionKey: key, EvidenceHMAC: testDigest(duplicate), RootType: "jsonl.content"},
		{SessionKey: key, EvidenceHMAC: testDigest(memoryText), RootType: "jsonl.tool_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Removed != 3 || outcome.Unavailable != 0 || outcome.Sessions != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Receipts) != 2 {
		t.Fatalf("receipts = %+v", outcome.Receipts)
	}
	for _, receipt := range outcome.Receipts {
		if receipt.Backend != "jsonl" || receipt.State != memstore.ReceiptRemoved {
			t.Fatalf("receipt = %+v", receipt)
		}
	}
	history, err := store.GetHistory(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "unrelated message" {
		t.Fatalf("history after cleanup = %+v", history)
	}
}

func TestLocatorCleanerReportsUnverifiableWithoutDeleting(t *testing.T) {
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	key := "session-unverifiable"
	if err := store.AddFullMessage(ctx, key, providers.Message{
		Role: "user", Content: "surviving message",
	}); err != nil {
		t.Fatal(err)
	}
	cleaner := NewLocatorCleaner(store, testDigest)
	outcome, err := cleaner.CleanLocators(ctx, []memstore.SourceLocator{{
		SessionKey: key, EvidenceHMAC: testDigest("forgotten fragment that is not a whole message"),
		RootType: "jsonl.content",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Removed != 0 || outcome.Unavailable != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.Receipts) != 1 || outcome.Receipts[0].State != memstore.ReceiptUnavailable {
		t.Fatalf("receipts = %+v", outcome.Receipts)
	}
	history, err := store.GetHistory(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Content != "surviving message" {
		t.Fatalf("unverifiable cleanup changed history: %+v", history)
	}
}
