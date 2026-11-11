// Tests for Chinese keyword extraction used by memory recall.
// 记忆召回使用的中文关键词提取测试。
package domain

import (
	"strings"
	"testing"
)

// TestQueryKeywordsExtractsChineseBigrams verifies Han runs become overlapping
// two-rune windows so a longer sentence can match a stored memory fragment.
// TestQueryKeywordsExtractsChineseBigrams 验证中文连续文本切成重叠双字词，
// 让较长句子能命中已存记忆片段。
func TestQueryKeywordsExtractsChineseBigrams(t *testing.T) {
	tokens := QueryKeywords("我喜欢吃苹果")
	want := map[string]bool{"喜欢": true, "苹果": true}
	for _, token := range tokens {
		delete(want, token)
	}
	if len(want) != 0 {
		t.Fatalf("missing keyword tokens %v in %v", want, tokens)
	}
}

// TestQueryKeywordsDropsStopWordsAndCaps verifies low-signal bigrams are removed
// and the result never exceeds KeywordLimit.
// TestQueryKeywordsDropsStopWordsAndCaps 验证低信息量双字词被过滤，结果不超过上限。
func TestQueryKeywordsDropsStopWordsAndCaps(t *testing.T) {
	for _, token := range QueryKeywords("这是什么 这个怎么弄") {
		switch token {
		case "什么", "这个", "怎么":
			t.Fatalf("stop word %q leaked into %v", token, QueryKeywords("这是什么 这个怎么弄"))
		}
	}
	tokens := QueryKeywords(strings.Repeat("长", 40))
	if len(tokens) > KeywordLimit {
		t.Fatalf("tokens = %d, want <= %d", len(tokens), KeywordLimit)
	}
}

// TestQueryKeywordsKeepsASCIIWords verifies ASCII words stay whole while a lone
// Han function character is ignored.
// TestQueryKeywordsKeepsASCIIWords 验证 ASCII 单词整体保留，单个汉字虚词被忽略。
func TestQueryKeywordsKeepsASCIIWords(t *testing.T) {
	tokens := QueryKeywords("Go 和 Rust 的性能")
	want := map[string]bool{"go": true, "rust": true}
	for _, token := range tokens {
		delete(want, token)
	}
	if len(want) != 0 {
		t.Fatalf("missing ASCII tokens %v in %v", want, tokens)
	}
}
