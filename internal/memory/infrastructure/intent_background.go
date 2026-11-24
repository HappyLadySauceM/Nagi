package infrastructure

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	"github.com/google/uuid"
)

func (r *Repo) IntentFacts(ctx context.Context, botID, groupID int64, conversationID, query, topicQuery string, subjects, fixedIDs []string) ([]contracts.IntentFact, error) {
	const limit = 4
	if _, err := uuid.Parse(conversationID); err != nil || botID <= 0 || groupID <= 0 {
		return nil, nil
	}
	if len(subjects) != 1 {
		return nil, nil
	}
	if id, err := strconv.ParseInt(subjects[0], 10, 64); err != nil || id <= 0 || id == botID {
		return nil, nil
	}
	args := []any{botID, groupID, conversationID, subjects}
	filters := make([]string, 0, domain.KeywordLimit)
	relevance := ""
	if fixedIDs != nil {
		if len(fixedIDs) > limit {
			return nil, nil
		}
		for _, id := range fixedIDs {
			if _, err := uuid.Parse(id); err != nil {
				return nil, nil
			}
			args = append(args, id)
			filters = append(filters, fmt.Sprintf("m.memory_id = $%d::uuid", len(args)))
		}
	} else {
		seen := make(map[string]bool)
		for _, text := range []string{query, topicQuery} {
			for _, keyword := range domain.QueryKeywords(text) {
				if keyword.Common || seen[keyword.Term] {
					continue
				}
				seen[keyword.Term] = true
				args = append(args, keyword.Term)
				filters = append(filters, fmt.Sprintf("strpos(lower(m.content), $%d) > 0", len(args)))
			}
		}
		if len(filters) > 0 {
			matches := make([]string, 0, len(filters))
			for _, filter := range filters {
				matches = append(matches, "("+filter+")::int")
			}
			relevance = "(" + strings.Join(matches, " + ") + ") DESC, "
		}
	}
	if len(filters) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
  SELECT m.memory_id::text, m.content, m.updated_at, m.fact_key,
    m.visibility, m.owner_key, m.subject_type, m.subject_id
  FROM memories m
  WHERE m.bot_id = $1
    AND m.status = 'active' AND btrim(m.approved_by) <> ''
    AND (m.expires_at IS NULL OR m.expires_at > statement_timestamp())
    AND char_length(m.content) BETWEEN 1 AND 240
    AND m.fact_key IS NOT NULL
    AND ((m.visibility = 'group' AND m.owner_key = $2::bigint::text)
      OR (m.visibility = 'conversation' AND m.owner_key = $3::uuid::text))
    AND `+factSupportPredicate("m", "", "$3::uuid")+`
    AND m.subject_type = 'user' AND m.subject_id = ANY($4::text[])
    AND (`+strings.Join(filters, " OR ")+`)
  ORDER BY `+relevance+`m.confidence DESC, m.updated_at DESC, m.memory_id
  LIMIT 4`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := make([]domain.Memory, 0, limit)
	for rows.Next() {
		m := domain.Memory{BotID: botID}
		if err := rows.Scan(&m.ID, &m.Content, &m.UpdatedAt, &m.FactKey, &m.Visibility, &m.OwnerKey, &m.SubjectType, &m.SubjectID); err != nil {
			return nil, err
		}
		facts = append(facts, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	valid := make([]contracts.IntentFact, 0, len(facts))
	for _, fact := range facts {
		sources, err := readFactSourcesInConversation(ctx, r.pool, fact, conversationID)
		if err != nil {
			return nil, err
		}
		if len(sources) > 0 {
			projectFactSources(&fact, sources)
			valid = append(valid, contracts.IntentFact{ID: fact.ID, Content: fact.Content, UpdatedAt: fact.UpdatedAt, SourceMessageID: fact.SourceMessageID})
		}
	}
	return valid, nil
}
