package memstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"strings"
	"unicode"
)

// Normalize produces a comparison key: NFKC-ish width folding, lower case,
// whitespace/punctuation removal. The original text is always kept separately.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		// Full-width ASCII -> half-width.
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ContentHash is the exact-duplicate key.
func ContentHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func bigrams(s string) map[string]struct{} {
	runes := []rune(s)
	out := make(map[string]struct{}, len(runes))
	if len(runes) < 2 {
		if len(runes) == 1 {
			out[string(runes[0])] = struct{}{}
		}
		return out
	}
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}

// Jaccard compares two normalized strings by rune bigram overlap.
func Jaccard(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	sa, sb := bigrams(a), bigrams(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	inter := 0
	for k := range sa {
		if _, ok := sb[k]; ok {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// SimHash returns a 64-bit locality-sensitive hash over rune bigrams.
func SimHash(normalized string) int64 {
	grams := bigrams(normalized)
	if len(grams) == 0 {
		return 0
	}
	var vec [64]int
	for gram := range grams {
		h := fnv.New64a()
		_, _ = h.Write([]byte(gram))
		v := h.Sum64()
		for i := 0; i < 64; i++ {
			if v&(1<<uint(i)) != 0 {
				vec[i]++
			} else {
				vec[i]--
			}
		}
	}
	var out uint64
	for i := 0; i < 64; i++ {
		if vec[i] > 0 {
			out |= 1 << uint(i)
		}
	}
	return int64(out)
}

// Hamming returns the bit distance between two SimHashes.
func Hamming(a, b int64) int {
	return bitsOnes(uint64(a) ^ uint64(b))
}

func bitsOnes(v uint64) int {
	n := 0
	for v != 0 {
		v &= v - 1
		n++
	}
	return n
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "000000"
	}
	return hex.EncodeToString(buf)
}

// TemplateKeyForSummary folds known repetitive legacy tasks.
func TemplateKeyForSummary(summary string) string {
	s := strings.TrimSpace(summary)
	switch {
	case strings.HasPrefix(s, "请分析这段对话，判断是否包含值得长期记住"),
		strings.HasPrefix(s, "请分析这段对话，判断是否值得长期记住"):
		return "memory_extract_prompt_v1"
	case strings.HasPrefix(s, "[长期记忆·用户置顶的背景信息"):
		return "pinned_memory_prefix_v1"
	case strings.HasPrefix(s, "[你现在扮演"):
		return "roleplay_prefix_v1"
	}
	return ""
}

// IsChitchat reports trivial messages that should not become memories.
func IsChitchat(summary string) bool {
	switch strings.ToLower(strings.TrimSpace(summary)) {
	case "hello", "hi", "hey", "你好", "你好吗", "在吗", "今天吃什么", "你可以做什么", "你是谁", "你的模型是什么":
		return true
	}
	return false
}
