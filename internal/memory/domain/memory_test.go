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
		delete(want, token.Term)
	}
	if len(want) != 0 {
		t.Fatalf("missing keyword tokens %v in %v", want, tokens)
	}
}

// TestQueryKeywordsMarksCommonWordsAndCaps verifies low-signal words stay as
// auxiliary Common terms and the result never exceeds KeywordLimit.
// TestQueryKeywordsMarksCommonWordsAndCaps 验证低信息词保留为辅助 Common 词，
// 且结果不超过上限。
func TestQueryKeywordsMarksCommonWordsAndCaps(t *testing.T) {
	got := QueryKeywords("这是什么 这个怎么弄")
	foundCommon := map[string]bool{}
	for _, token := range got {
		if token.Term == "什么" || token.Term == "这个" || token.Term == "怎么" {
			if !token.Common {
				t.Fatalf("term %q must be Common: %+v", token.Term, got)
			}
			foundCommon[token.Term] = true
		}
	}
	if len(foundCommon) != 3 {
		t.Fatalf("common terms = %v in %+v", foundCommon, got)
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
	tokens := QueryKeywords("Go和Rust 的性能")
	want := map[string]bool{"go": true, "rust": true}
	for _, token := range tokens {
		delete(want, token.Term)
	}
	if len(want) != 0 {
		t.Fatalf("missing ASCII tokens %v in %v", want, tokens)
	}
}
