package memstore

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// BenchmarkHybridRetrieve reports a p95 alongside the mean. The plan's gate is
// p95 < 15ms on a few hundred entries.
func BenchmarkHybridRetrieve(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "memory.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		if _, err := s.Add(ctx, AddInput{
			Text:  fmt.Sprintf("评测记忆 %d：关于边界授权、尺度与氛围的说明", i),
			Layer: LayerProcedural, Realm: RealmFictional, Audience: "private",
			Predicate: "写作规则", Facet: fmt.Sprintf("分档-%d", i), Importance: 0.7,
		}); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := s.Add(ctx, AddInput{
		Text: "T0 铁律：边界授权", Layer: LayerProcedural, Realm: RealmFictional,
		Audience: "private", Predicate: "写作规则", Facet: "边界授权",
		Importance: 0.8, PinnedCore: true,
	}); err != nil {
		b.Fatal(err)
	}

	samples := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		if _, err := s.SearchEntries(ctx, SearchOptions{Query: "边界授权", Limit: 20}); err != nil {
			b.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95 := samples[len(samples)*95/100]
	b.ReportMetric(float64(p95)/float64(time.Millisecond), "p95-ms")
}
