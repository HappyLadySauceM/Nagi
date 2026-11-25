package infrastructure

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
)

const historyQuery = `
SELECT m.id, m.sender_id, m.created_at, projected.body
FROM messages m
CROSS JOIN LATERAL (
    SELECT left(btrim(CASE
        WHEN CASE WHEN jsonb_typeof(m.segments) = 'array' THEN
                      jsonb_array_length(m.segments) = 1 AND m.segments->0->>'type' = 'text'
                  ELSE false END
            THEN CASE WHEN jsonb_typeof(m.segments->0->'data'->'text') = 'string'
                      THEN m.segments->0->'data'->>'text' ELSE '' END
        WHEN m.segments IN ('[]'::jsonb, 'null'::jsonb) OR m.segments @> $8::jsonb
            THEN regexp_replace(m.text_content, '\[CQ:[^]]*(\]|$)', '', 'g')
        WHEN jsonb_typeof(m.segments) <> 'array' THEN ''
        ELSE COALESCE((
            SELECT string_agg(segment.value->'data'->>'text', '' ORDER BY segment.position)
            FROM jsonb_array_elements(m.segments) WITH ORDINALITY AS segment(value, position)
            WHERE segment.value->>'type' = 'text' AND jsonb_typeof(segment.value->'data'->'text') = 'string'
        ), '') END, E' \t\n\r'), 4096) AS body OFFSET 0
) projected
CROSS JOIN LATERAL (SELECT lower(projected.body) AS normalized OFFSET 0) text
CROSS JOIN LATERAL (SELECT strpos(text.normalized, lower($6)) > 0 AS full_match OFFSET 0) phrase
CROSS JOIN LATERAL (
    SELECT CASE WHEN phrase.full_match THEN cardinality($7::text[])
                WHEN cardinality($7::text[]) = 1 THEN 0
                ELSE (SELECT count(*) FROM unnest($7::text[]) AS term(value)
                      WHERE strpos(text.normalized, lower(term.value)) > 0)
           END AS matched OFFSET 0
) score
WHERE m.conversation_id = $1::uuid AND m.bot_id = $2
  AND m.created_at >= $3 AND (m.created_at, m.id) < ($4, $5) AND m.id < $5
  AND m.control_kind = '' AND (m.direction = 'incoming' OR (m.direction = 'outgoing' AND m.sent_status = 'sent'))
  AND ($9::bigint IS NULL OR m.sender_id = $9) AND score.matched > 0
ORDER BY phrase.full_match DESC, score.matched DESC, m.created_at DESC, m.id DESC LIMIT $10`

func (r *Repo) SearchHistory(ctx context.Context, search domain.HistorySearch) ([]contracts.HistoryHit, error) {
	terms := strings.Fields(search.Query)
	unique := make([]string, 0, len(terms))
	seen := make(map[string]bool, len(terms))
	for _, term := range terms {
		key := strings.ToLower(term)
		if !seen[key] {
			unique = append(unique, term)
			seen[key] = true
		}
	}
	sender := sql.NullInt64{Int64: search.SenderID, Valid: search.SenderID > 0}
	transcript := `[{"type":"record","data":{"` + media.TranscriptMarker + `":"true"}}]`
	rows, err := r.pool.Query(ctx, historyQuery, search.ConversationID, search.BotID, search.After, search.Before,
		search.BeforeID, search.Query, unique, transcript, sender, search.Limit)
	if err != nil {
		return nil, fmt.Errorf("conversation: query history: %w", err)
	}
	defer rows.Close()
	var hits []contracts.HistoryHit
	for rows.Next() {
		var hit contracts.HistoryHit
		if err := rows.Scan(&hit.ID, &hit.SenderID, &hit.CreatedAt, &hit.Text); err != nil {
			return nil, fmt.Errorf("conversation: scan history: %w", err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("conversation: read history: %w", err)
	}
	return hits, nil
}
