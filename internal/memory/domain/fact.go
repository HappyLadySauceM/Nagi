package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Fact struct {
	Predicate string
	Value     string
	Nature    string
}

type FactProposal struct {
	Predicate string
	Value     string
	Evidence  string
}

type FactSource struct {
	MessageID int64
	SenderID  int64
}

type factSyntax struct {
	predicate string
	pattern   *regexp.Regexp
}

var factForms = []factSyntax{
	{"self_name", regexp.MustCompile(`^(?:我叫|我的名字是|我的昵称是)(.+)$`)},
	{"preferred_address", regexp.MustCompile(`^(?:我希望(?:大家|你们|你)叫我|我希望被称为|请(?:大家|你们|你)?叫我|(?:大家|你们|你)可以叫我)(.+)$`)},
	{"favorite_tea", regexp.MustCompile(`^(?:我最喜欢的茶是|茶里我最偏爱)(.+)$`)},
	{"favorite_tea", regexp.MustCompile(`^(.+?)是我最爱喝的茶$`)},
	{"preference_like", regexp.MustCompile(`^我喜欢(?:喝)?(.+)$`)},
}

var ambiguousFactMarkers = []string{"的话", "如果", "假如", "实际上", "其实不", "不喜欢", "不是", "可能", "也许", "听说", "开玩笑", "玩笑", "误会", "还是", "或者", "但是", "不过", "他叫", "她叫", "你叫", "他说", "她说", "你说", "不叫", "以前", "曾经", "改叫", "别叫", "并非", "转述", "哈哈", "假扮"}

func FactsFromSource(source string) []Fact {
	for _, marker := range ambiguousFactMarkers {
		if strings.Contains(source, marker) {
			return nil
		}
	}
	if strings.ContainsAny(source, "?？“”‘’\"「」『』\n\r@") {
		return nil
	}
	clauses := strings.FieldsFunc(source, func(r rune) bool { return strings.ContainsRune("，,。；;！!", r) })
	if len(clauses) == 0 || len(clauses) > 4 {
		return nil
	}
	facts := make([]Fact, 0, len(clauses))
	for _, clause := range clauses {
		fact, ok := parseFactClause(strings.TrimSpace(clause))
		if !ok {
			return nil
		}
		for _, existing := range facts {
			if existing.Predicate == fact.Predicate && existing != fact && fact.Predicate != "preference_like" {
				return nil
			}
		}
		facts = append(facts, fact)
	}
	return facts
}

func FactFromEvidence(source string, proposal FactProposal) (Fact, bool) {
	facts := FactsFromSource(source)
	if len(facts) == 0 {
		return Fact{}, false
	}
	if proposal == (FactProposal{}) {
		if len(facts) == 1 {
			return facts[0], true
		}
		return Fact{}, false
	}
	clause := strings.TrimSpace(proposal.Evidence)
	for _, candidate := range strings.FieldsFunc(source, func(r rune) bool { return strings.ContainsRune("，,。；;！!", r) }) {
		if strings.TrimSpace(candidate) == clause {
			fact, ok := parseFactClause(clause)
			return fact, ok && fact.Predicate == proposal.Predicate && fact.Value == normalizeFactValue(proposal.Value)
		}
	}
	return Fact{}, false
}

func FactSupportedBy(fact Fact, text string) bool {
	return slices.Contains(FactsFromSource(text), fact)
}

func FactFromContent(content string) (Fact, bool) {
	content = strings.TrimPrefix(strings.TrimSpace(content), "本人自述：")
	return FactFromEvidence(content, FactProposal{})
}

func parseFactClause(clause string) (Fact, bool) {
	for _, form := range factForms {
		parts := form.pattern.FindStringSubmatch(clause)
		if len(parts) == 2 {
			value := normalizeFactValue(parts[1])
			if value != "" {
				return Fact{Predicate: form.predicate, Value: value, Nature: "self_report"}, true
			}
		}
	}
	return Fact{}, false
}

func normalizeFactValue(value string) string {
	v := strings.Join(strings.Fields(value), " ")
	if v == "" || utf8.RuneCountInString(v) > 60 || strings.ContainsAny(v, "、/\\:：！？?!。，,；;\"“”()（）[]【】") {
		return ""
	}
	for _, part := range []string{"还有", "以及", "同时", "并且", "和我", "叫我", "来着", "这句话"} {
		if strings.Contains(v, part) {
			return ""
		}
	}
	return v
}

func (f Fact) Content() string {
	switch f.Predicate {
	case "self_name":
		return "本人自述：我叫" + f.Value
	case "preferred_address":
		return "本人自述：我希望被称为" + f.Value
	case "favorite_tea":
		return "本人自述：我最喜欢的茶是" + f.Value
	case "preference_like":
		return "本人自述：我喜欢" + f.Value
	}
	return ""
}

func (f Fact) Key(memory Memory, scope ExtractionScope) string {
	fields := []string{"fact.v1", strconv.FormatInt(memory.BotID, 10), string(memory.Visibility), memory.OwnerKey,
		scope.ConversationID, scope.ScopeType, strconv.FormatInt(scope.ScopeID, 10), strconv.FormatInt(scope.AuthorizationEventID, 10),
		string(memory.SubjectType), memory.SubjectID, f.Predicate, f.Value, f.Nature}
	raw, _ := json.Marshal(fields)
	sum := sha256.Sum256(raw)
	return "v1:" + hex.EncodeToString(sum[:])
}
