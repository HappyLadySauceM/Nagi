package domain

import (
	"sort"
	"strconv"
	"strings"
)

// SubjectMatch values describe how one memory's subject relates to a request.
// SubjectMatch 描述一条记忆主体与请求主体之间的关系。
const (
	SubjectMatchExact  = "exact"
	SubjectMatchGroup  = "group"
	SubjectMatchShared = "shared"
	SubjectMatchOther  = "other"
	SubjectMatchNone   = "none"
)

// SelectionReason values explain why one memory was selected.
// SelectionReason 解释一条记忆被选入的原因。
const (
	SelectionReasonSubjectExact          = "subject_exact"
	SelectionReasonGroupMatch            = "group_match"
	SelectionReasonKeywordMatch          = "keyword_match"
	SelectionReasonSharedFallback        = "shared_fallback"
	SelectionReasonUnknownSubjectKeyword = "unknown_subject_keyword"
)

// RecallQuery is the domain-level subject and keyword filter for recall.
// RecallQuery 是召回使用的领域级主体与关键词过滤条件。
type RecallQuery struct {
	Kind        string
	TargetID    int64
	Query       string
	SubjectType SubjectType
	SubjectID   string
	Limit       int
}

// RecallHit is one ranked memory with its scoring audit fields.
// RecallHit 是一条带评分审计字段的记忆命中。
type RecallHit struct {
	Memory          Memory
	Score           float64
	HitKeywords     []string
	SubjectMatch    string
	SelectionReason string
}

// RankRecall filters by non-common keyword hits, applies subject-aware ordering,
// and returns diagnostics that distinguish missing keywords from no matches.
// RankRecall 按非通用关键词命中过滤，应用主体感知排序，并返回可区分
// 「无有效关键词」与「有关键词但无命中」的诊断信息。
func RankRecall(memories []Memory, query RecallQuery) ([]RecallHit, bool, string) {
	terms := QueryKeywords(query.Query)
	validTerms := 0
	for _, term := range terms {
		if !term.Common {
			validTerms++
		}
	}
	if validTerms == 0 {
		return nil, true, "no_keyword"
	}

	hits := make([]RecallHit, 0, len(memories))
	for _, memory := range memories {
		match := subjectMatch(query, memory)
		if match == "" {
			continue
		}
		matched := make([]QueryTerm, 0, len(terms))
		for _, term := range terms {
			if containsFold(memory.Content, term.Term) {
				matched = append(matched, term)
			}
		}
		hasValidHit := false
		for _, term := range matched {
			if !term.Common {
				hasValidHit = true
				break
			}
		}
		if !hasValidHit {
			continue
		}
		score := 0.0
		hitKeywords := make([]string, 0, len(matched))
		for _, term := range matched {
			if term.Common {
				score += 0.25
			} else {
				score += 1.0
			}
			hitKeywords = append(hitKeywords, term.Term)
		}
		if score > 3.0 {
			score = 3.0
		}
		hits = append(hits, RecallHit{
			Memory:       memory,
			Score:        score + subjectWeight(match),
			HitKeywords:  hitKeywords,
			SubjectMatch: match,
		})
	}
	if len(hits) == 0 {
		return nil, true, "no_match"
	}

	hasExactOrGroup := false
	for _, hit := range hits {
		if hit.SubjectMatch == SubjectMatchExact || hit.SubjectMatch == SubjectMatchGroup {
			hasExactOrGroup = true
			break
		}
	}
	for i := range hits {
		hits[i].SelectionReason = selectionReason(query, hits[i].SubjectMatch, hasExactOrGroup)
	}

	sort.Slice(hits, func(i, j int) bool {
		left, right := hits[i], hits[j]
		leftRank := subjectRank(left.SubjectMatch)
		rightRank := subjectRank(right.SubjectMatch)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if !left.Memory.UpdatedAt.Equal(right.Memory.UpdatedAt) {
			return left.Memory.UpdatedAt.After(right.Memory.UpdatedAt)
		}
		return left.Memory.ID < right.Memory.ID
	})
	if query.Limit > 0 && len(hits) > query.Limit {
		hits = hits[:query.Limit]
	}
	return hits, false, ""
}

// subjectMatch evaluates the contract's subject priority for one memory.
// subjectMatch 按契约优先级评估一条记忆的主体匹配等级。
func subjectMatch(query RecallQuery, memory Memory) string {
	if query.SubjectType == SubjectUser {
		if memory.SubjectType == SubjectUser && strings.TrimSpace(query.SubjectID) != "" &&
			strings.TrimSpace(query.SubjectID) == strings.TrimSpace(memory.SubjectID) {
			return SubjectMatchExact
		}
		return ""
	}
	groupID := strconv.FormatInt(query.TargetID, 10)
	if query.SubjectType == SubjectGroup && strings.TrimSpace(query.SubjectID) != groupID {
		return ""
	}
	if query.Kind == "group" && memory.SubjectType == SubjectGroup &&
		strings.TrimSpace(memory.SubjectID) == groupID {
		if query.SubjectType == SubjectGroup {
			return SubjectMatchExact
		}
		return SubjectMatchGroup
	}
	if memory.Visibility == VisibilityBotShared && (memory.SubjectType == "" || memory.SubjectType == SubjectUnknown) &&
		strings.TrimSpace(memory.SubjectID) == "" {
		return SubjectMatchShared
	}
	return ""
}

func subjectWeight(match string) float64 {
	switch match {
	case SubjectMatchExact:
		return 2.0
	case SubjectMatchGroup:
		return 1.5
	case SubjectMatchShared:
		return 0.5
	default:
		return 0
	}
}

func subjectRank(match string) int {
	switch match {
	case SubjectMatchExact:
		return 0
	case SubjectMatchGroup:
		return 1
	case SubjectMatchShared:
		return 2
	case SubjectMatchOther:
		return 3
	default:
		return 4
	}
}

func selectionReason(query RecallQuery, match string, hasExactOrGroup bool) string {
	switch match {
	case SubjectMatchExact:
		return SelectionReasonSubjectExact
	case SubjectMatchGroup:
		return SelectionReasonGroupMatch
	case SubjectMatchShared:
		if hasExactOrGroup {
			return SelectionReasonKeywordMatch
		}
		return SelectionReasonSharedFallback
	}
	if query.SubjectType == "" || query.SubjectType == SubjectUnknown {
		return SelectionReasonUnknownSubjectKeyword
	}
	return SelectionReasonKeywordMatch
}

func containsFold(content, term string) bool {
	return strings.Contains(strings.ToLower(content), strings.ToLower(term))
}
