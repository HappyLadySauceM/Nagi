package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/conversation/domain"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	scope "github.com/HappyLadySauceM/Nagi/internal/scope/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"github.com/HappyLadySauceM/Nagi/pkg/db/ent"
)

const topicRoutingDeadline = 30 * time.Second

func registerTopicRoute(ctx context.Context, client *ent.Client, conversation uuid.UUID, message int64) error {
	// The caller holds the source conversation lock. Allocate no independent ordering key.
	// 调用方持有源会话锁，沿用入站 ID 表示同一会话的提交顺序。
	if _, err := client.ExecContext(ctx, `INSERT INTO group_topic_routes (id, conversation_id, deadline)
 VALUES ($1, $2, clock_timestamp() + make_interval(secs => $3))`, message, conversation, topicRoutingDeadline.Seconds()); err != nil {
		return err
	}
	_, err := client.ExecContext(ctx, `INSERT INTO group_topic_route_candidates (id, conversation_id, route_id, topic_id, version)
 SELECT gen_random_uuid(), t.conversation_id, $1, t.id, t.version
 FROM group_topics t JOIN messages m ON m.id=$1 AND m.conversation_id=t.conversation_id
 WHERE t.conversation_id=$2 AND NOT t.closed AND t.expires_at>m.created_at
 ORDER BY t.id`, message, conversation)
	return err
}

// ClaimTopicRoute takes at most one eligible head of a source conversation without taking a generation slot.
// ClaimTopicRoute 最多领取一个会话队首的可用标记，不占用生成槽。
func (r *Repo) ClaimTopicRoute(ctx context.Context, lease time.Duration) (*domain.TopicRouteClaim, error) {
	if lease < time.Millisecond {
		return nil, fmt.Errorf("conversation: topic route lease must be at least one millisecond")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var conversation string
	err = tx.QueryRow(ctx, `SELECT c.id FROM conversations c
 JOIN LATERAL (SELECT status, deadline, lease_expires_at FROM group_topic_routes
  WHERE conversation_id=c.id AND status IN ('pending','routing') ORDER BY id LIMIT 1) head ON true
 WHERE c.kind='group' AND (head.deadline<=clock_timestamp() OR head.status='pending' OR head.lease_expires_at<=clock_timestamp())
 ORDER BY head.deadline, c.id LIMIT 1 FOR UPDATE OF c SKIP LOCKED`).Scan(&conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Re-read after acquiring the conversation lock under READ COMMITTED.
	// 在 READ COMMITTED 下取得会话锁后重新读取。
	if _, err := tx.Exec(ctx, `UPDATE group_topic_routes
 SET status='expired', claim_token=NULL, lease_expires_at=NULL
 WHERE conversation_id=$1 AND status IN ('pending','routing') AND deadline<=clock_timestamp()`, conversation); err != nil {
		return nil, err
	}
	var claim domain.TopicRouteClaim
	claim.ConversationID, claim.Token = conversation, uuid.NewString()
	err = tx.QueryRow(ctx, `WITH head AS (
   SELECT id FROM group_topic_routes WHERE conversation_id=$1 AND status IN ('pending','routing') ORDER BY id LIMIT 1
 ) UPDATE group_topic_routes r SET status='routing', claim_token=$2,
   lease_expires_at=LEAST(deadline, clock_timestamp()+make_interval(secs => $3))
 FROM head WHERE r.id=head.id AND r.deadline>clock_timestamp()
   AND (r.status='pending' OR r.lease_expires_at<=clock_timestamp())
 RETURNING r.id, r.deadline, r.lease_expires_at`, conversation, claim.Token, lease.Seconds()).Scan(&claim.MessageID, &claim.Deadline, &claim.LeaseExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	// An earlier route in the same conversation may have advanced a candidate before this lease.
	// 本会话中更早的归属可能已推进候选版本，领取时重新取证。
	if _, err := tx.Exec(ctx, `UPDATE group_topic_route_candidates p SET version=t.version
	 FROM group_topics t WHERE p.route_id=$1 AND t.id=p.topic_id AND t.conversation_id=p.conversation_id
	 AND NOT t.closed AND t.expires_at>(SELECT created_at FROM messages WHERE id=p.route_id)`, claim.MessageID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT p.topic_id, p.version FROM group_topic_route_candidates p
	 JOIN group_topics t ON t.id=p.topic_id AND t.conversation_id=p.conversation_id
	 WHERE p.route_id=$1 AND NOT t.closed AND t.expires_at>(SELECT created_at FROM messages WHERE id=p.route_id) ORDER BY p.topic_id`, claim.MessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate domain.TopicRouteCandidate
		if err := rows.Scan(&candidate.TopicID, &candidate.Version); err != nil {
			return nil, err
		}
		claim.Candidates = append(claim.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &claim, nil
}

// ResolveTopicRoute commits the trusted result once in source-conversation order and fences late claims.
// ResolveTopicRoute 按源会话顺序提交一次可信归属结果，并拦截迟到的旧领取。
func (r *Repo) ResolveTopicRoute(ctx context.Context, claim domain.TopicRouteClaim, resolution domain.TopicRouteResolution) (domain.TopicRouteResult, error) {
	var result domain.TopicRouteResult
	action, topicID := resolution.Action, resolution.TopicID
	switch action {
	case domain.TopicRouteUnrelated, domain.TopicRouteUnavailable:
		if topicID != 0 {
			return result, fmt.Errorf("conversation: terminal topic route cannot name a topic")
		}
	case domain.TopicRouteOpen, domain.TopicRouteWaiting:
		if topicID != 0 || resolution.ExpiresAt.IsZero() {
			return result, fmt.Errorf("conversation: invalid topic opening")
		}
	case domain.TopicRouteFollowUp, domain.TopicRouteContribution, domain.TopicRouteCorrection,
		domain.TopicRouteReplacement, domain.TopicRouteMerge, domain.TopicRouteClose:
		if topicID <= 0 {
			return result, fmt.Errorf("conversation: invalid topic route target")
		}
	default:
		return result, fmt.Errorf("conversation: invalid topic route action")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var conversation string
	err = tx.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 AND kind='group' FOR UPDATE`, claim.ConversationID).Scan(&conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return result, domain.ErrTopicRouteClaimLost
	}
	if err != nil {
		return result, err
	}
	var version, expected int64
	var expiresAt time.Time
	if topicID != 0 {
		err = tx.QueryRow(ctx, `SELECT t.version, t.expires_at, p.version
  FROM group_topics t JOIN group_topic_route_candidates p ON p.topic_id=t.id AND p.conversation_id=t.conversation_id
  WHERE t.conversation_id=$1 AND t.id=$2 AND p.route_id=$3 AND NOT t.closed
  AND t.expires_at>(SELECT created_at FROM messages WHERE id=p.route_id) FOR UPDATE OF t`, conversation, topicID, claim.MessageID).Scan(&version, &expiresAt, &expected)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM group_topic_routes
 WHERE id=$1 AND conversation_id=$2 AND status='routing' AND claim_token=$3
 AND lease_expires_at>clock_timestamp() AND deadline>clock_timestamp()
 AND NOT EXISTS (SELECT 1 FROM group_topic_routes p WHERE p.conversation_id=$2
 AND p.id<$1 AND p.status IN ('pending','routing')) FOR UPDATE`, claim.MessageID, conversation, claim.Token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return result, domain.ErrTopicRouteClaimLost
	}
	if err != nil {
		return result, err
	}
	switch {
	case action == domain.TopicRouteUnrelated || action == domain.TopicRouteUnavailable:
		result.Status = string(action)
	case topicID != 0 && (version == 0 || version != expected):
		result.Status = "stale"
	case topicID == 0:
		if err := tx.QueryRow(ctx, `INSERT INTO group_topics (id,conversation_id,version,expires_at)
  SELECT j.id,$2,1,$3 FROM message_jobs j WHERE j.incoming_message_id=$1
  RETURNING id,version`, id, conversation, resolution.ExpiresAt).Scan(&topicID, &version); err != nil {
			return result, err
		}
		// Later queued messages may already have been committed while this topic was being opened.
		// 开题过程中，较晚的入站可能已提交。为这些仍待归属的消息补登候选。
		if _, err := tx.Exec(ctx, `INSERT INTO group_topic_route_candidates (id,conversation_id,route_id,topic_id,version)
		 SELECT gen_random_uuid(), conversation_id, id, $3, $4 FROM group_topic_routes
		 WHERE conversation_id=$1 AND id>$2 AND status='pending' AND deadline>clock_timestamp()
		 ON CONFLICT (route_id,topic_id) DO NOTHING`, conversation, id, topicID, version); err != nil {
			return result, err
		}
		result = domain.TopicRouteResult{Status: "resolved", TopicID: topicID, Version: version}
	default:
		if action != domain.TopicRouteClose && resolution.ExpiresAt.After(expiresAt) {
			expiresAt = resolution.ExpiresAt
		}
		if err := tx.QueryRow(ctx, `UPDATE group_topics SET version=version+1, closed=$3, expires_at=$4
  WHERE id=$1 AND conversation_id=$2 RETURNING version`, topicID, conversation, action == domain.TopicRouteClose, expiresAt).Scan(&version); err != nil {
			return result, err
		}
		result = domain.TopicRouteResult{Status: "resolved", TopicID: topicID, Version: version}
	}
	if result.TopicID != 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO group_topic_inputs (id,conversation_id,topic_id,version,route_id,action)
  VALUES (gen_random_uuid(),$1,$2,$3,$4,$5)`, conversation, result.TopicID, result.Version, id, string(action)); err != nil {
			return domain.TopicRouteResult{}, err
		}
	}
	if err := finishTopicRoute(ctx, tx, claim, result.Status); err != nil {
		return domain.TopicRouteResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.TopicRouteResult{}, err
	}
	return result, nil
}

func finishTopicRoute(ctx context.Context, tx *db.Tx, claim domain.TopicRouteClaim, status string) error {
	result, err := tx.Exec(ctx, `UPDATE group_topic_routes SET status=$4, claim_token=NULL, lease_expires_at=NULL
 WHERE id=$1 AND conversation_id=$2 AND claim_token=$3 AND lease_expires_at>clock_timestamp() AND deadline>clock_timestamp()`,
		claim.MessageID, claim.ConversationID, claim.Token, status)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domain.ErrTopicRouteClaimLost
	}
	return nil
}

// RouteNext deterministically attributes a queued input outside the generation pool.
// RouteNext 独立于生成池，为待归属入站核对确定性证据。
func (r *Repo) RouteNext(ctx context.Context) (bool, error) {
	claim, err := r.ClaimTopicRoute(ctx, 2*time.Second)
	if err != nil || claim == nil {
		return false, err
	}
	if err := r.routeClaim(ctx, *claim); err != nil && !errors.Is(err, domain.ErrTopicRouteClaimLost) {
		return true, err
	}
	return true, nil
}

func (r *Repo) routeClaim(ctx context.Context, claim domain.TopicRouteClaim) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var botID, targetID int64
	if err := tx.QueryRow(ctx, `SELECT bot_id,target_id FROM conversations WHERE id=$1 AND kind='group'`, claim.ConversationID).Scan(&botID, &targetID); err != nil {
		return err
	}
	if err := scope.LockPolicies(ctx, tx, false, scope.PolicyLockKey{BotID: botID, Kind: "group"}); err != nil {
		return err
	}
	var source string
	if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE id=$1 AND kind='group' AND bot_id=$2 AND target_id=$3 FOR UPDATE`, claim.ConversationID, botID, targetID).Scan(&source); err != nil {
		return err
	}
	var sender int64
	var wire []byte
	var authorized bool
	if err := tx.QueryRow(ctx, `SELECT m.sender_id,m.segments,COALESCE((SELECT enabled AND store_messages AND allow_history_read FROM scope_policies
 WHERE bot_id=m.bot_id AND scope_type='group' AND scope_id IN (0,$4) ORDER BY scope_id DESC LIMIT 1),false)
 FROM group_topic_routes r JOIN messages m ON (m.id,m.conversation_id)=(r.id,r.conversation_id)
 WHERE r.id=$1 AND r.conversation_id=$2 AND r.claim_token=$3 AND r.status='routing'
 AND r.deadline>clock_timestamp() AND r.lease_expires_at>clock_timestamp()`, claim.MessageID, source, claim.Token, targetID).Scan(&sender, &wire, &authorized); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrTopicRouteClaimLost
		}
		return err
	}
	if !authorized {
		return concludeTopicRoute(ctx, tx, claim, "unavailable", 0, "")
	}
	segments, err := decodeSegments(wire)
	if err != nil {
		return concludeTopicRoute(ctx, tx, claim, "unavailable", 0, "")
	}
	reference, validReference, image, body, addressedElsewhere, addressedBot := routeSignals(segments, botID)
	if addressedElsewhere && !validReference {
		return concludeTopicRoute(ctx, tx, claim, "unrelated", 0, "")
	}
	var topicID int64
	action := "follow_up"
	if validReference {
		var controlOnly, ownsCurrentReference, hasQueuedReply bool
		err = tx.QueryRow(ctx, `WITH referenced AS (
  SELECT id,direction,control_kind,outbound_id,platform_message_id,sender_id
  FROM messages
  WHERE conversation_id=$1 AND bot_id=$2 AND platform_message_id=$3
   AND ((direction='incoming' AND id<$4) OR (direction='outgoing' AND sent_status='sent'))
  ), quoted AS (
  SELECT p.id,p.direction,j.incoming_message_id AS outbound_source,p.sender_id
  FROM referenced p LEFT JOIN agent_outbound_messages o ON o.id=p.outbound_id AND o.status='sent'
   AND o.conversation_id=$1 AND o.bot_id=$2 AND o.platform_message_id=p.platform_message_id
  LEFT JOIN message_jobs j ON j.id=o.job_id
  WHERE p.control_kind='' AND (p.direction='incoming' OR j.incoming_message_id<$4)
  ), topics AS (
   SELECT DISTINCT i.topic_id,i.version=t.version AND q.direction='incoming' AND q.sender_id=$5 AS own_current FROM quoted q JOIN group_topic_inputs i ON i.conversation_id=$1
   AND i.route_id=CASE WHEN q.direction='incoming' THEN q.id ELSE q.outbound_source END
   JOIN group_topics t ON (t.id,t.conversation_id)=(i.topic_id,i.conversation_id)
   WHERE NOT t.closed AND t.expires_at>(SELECT created_at FROM messages WHERE id=$4)
  ) SELECT CASE WHEN count(*)=1 THEN min(topic_id) ELSE 0 END,
   EXISTS(SELECT 1 FROM referenced WHERE control_kind<>'') AND NOT EXISTS(SELECT 1 FROM referenced WHERE control_kind=''),
   (SELECT count(*)=1 FROM quoted) AND COALESCE(bool_and(own_current),false),
   EXISTS(SELECT 1 FROM topics q JOIN group_topic_inputs i ON i.topic_id=q.topic_id
     JOIN message_jobs j ON j.incoming_message_id=i.route_id WHERE j.status IN ('pending','processing') AND i.route_id<$4
     AND (SELECT decision FROM intent_decisions WHERE job_id=j.id ORDER BY id DESC LIMIT 1)='reply')
   FROM topics`, source, botID, reference, claim.MessageID, sender).Scan(&topicID, &controlOnly, &ownsCurrentReference, &hasQueuedReply)
		if err != nil {
			return err
		}
		if topicID != 0 {
			switch {
			case ownsCurrentReference && isTopicCorrection(body):
				action = "corrected"
			case isDirectQuestion(body) && !addressedElsewhere:
				return concludeTopicRoute(ctx, tx, claim, "unrelated", 0, "")
			case hasQueuedReply && strings.HasPrefix(body, "补充"):
				action = "contribution"
			case hasQueuedReply:
				topicID = 0
			}
		}
		if controlOnly {
			return concludeTopicRoute(ctx, tx, claim, "unrelated", 0, "")
		}
	}
	if topicID == 0 && !validReference && (image || body != "") && !addressedElsewhere {
		rows, err := tx.Query(ctx, `SELECT t.id,previous.text_content
  FROM group_topics t JOIN group_topic_inputs i ON (i.topic_id,i.conversation_id,i.version)=(t.id,t.conversation_id,t.version)
  JOIN messages previous ON previous.id=i.route_id AND previous.conversation_id=i.conversation_id
  JOIN messages current ON current.id=$2 AND current.conversation_id=t.conversation_id
  WHERE t.conversation_id=$1 AND NOT t.closed AND t.expires_at>current.created_at
  AND i.action='waiting' AND previous.control_kind='' AND previous.sender_id=$3 AND previous.id<current.id
  AND current.created_at>=previous.created_at AND current.created_at<previous.created_at+interval '30 seconds'`, source, claim.MessageID, sender)
		if err != nil {
			return err
		}
		matches := 0
		for rows.Next() {
			var id int64
			var request string
			if err := rows.Scan(&id, &request); err != nil {
				rows.Close()
				return err
			}
			if matchesRequestedMaterial(request, body, image) {
				topicID = id
				matches++
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if matches == 1 {
			action = "contribution"
		} else {
			topicID = 0
		}
	}
	if topicID != 0 {
		return concludeTopicRoute(ctx, tx, claim, "resolved", topicID, action)
	}
	var candidates, earlierUndecided bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM group_topic_route_candidates p JOIN group_topics t ON (t.id,t.conversation_id)=(p.topic_id,p.conversation_id)
 WHERE p.route_id=$1 AND NOT t.closed AND t.expires_at>(SELECT created_at FROM messages WHERE id=p.route_id)
 AND (NOT $3 OR EXISTS(SELECT 1 FROM group_topic_inputs i JOIN message_jobs active ON active.incoming_message_id=i.route_id
 WHERE (i.topic_id,i.conversation_id)=(t.id,t.conversation_id) AND i.version<=t.version
 AND (active.status='processing' OR (active.status='pending' AND active.delivery_continuation))))
 ), EXISTS (
 SELECT 1 FROM messages m JOIN message_jobs j ON j.incoming_message_id=m.id
 WHERE m.conversation_id=$2 AND m.id<$1 AND j.status IN ('pending','processing')
 AND NOT EXISTS (SELECT 1 FROM intent_decisions d WHERE d.job_id=j.id)
 )`, claim.MessageID, source, !validReference && image && body == "").Scan(&candidates, &earlierUndecided); err != nil {
		return err
	}
	if !validReference && (body == "" && !image || addressedBot && (isDirectQuestion(body) || announcesTopicMaterial(body))) || addressedElsewhere || !candidates && !earlierUndecided {
		return concludeTopicRoute(ctx, tx, claim, "unrelated", 0, "")
	}
	// No deterministic evidence can distinguish these candidates. Leave the original deadline in force.
	// 现有确定性证据无法区分候选，保留原始截止时间。
	return tx.Commit(ctx)
}

func concludeTopicRoute(ctx context.Context, tx *db.Tx, claim domain.TopicRouteClaim, status string, topicID int64, action string) error {
	if topicID != 0 {
		var version, expected int64
		for _, candidate := range claim.Candidates {
			if candidate.TopicID == topicID {
				expected = candidate.Version
				break
			}
		}
		if err := tx.QueryRow(ctx, `UPDATE group_topics SET version=version+1
   WHERE id=$1 AND conversation_id=$2 AND version=$3 AND NOT closed AND expires_at>(SELECT created_at FROM messages WHERE id=$4)
   AND NOT EXISTS (SELECT 1 FROM group_topic_inputs i JOIN messages later ON later.id=i.route_id
     JOIN messages current ON current.id=$4 WHERE i.topic_id=$1 AND i.conversation_id=$2
     AND (later.created_at,later.id)>=(current.created_at,current.id)) RETURNING version`, topicID, claim.ConversationID, expected, claim.MessageID).Scan(&version); errors.Is(err, sql.ErrNoRows) {
			return tx.Commit(ctx)
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO group_topic_inputs(id,conversation_id,topic_id,version,route_id,action)
   VALUES(gen_random_uuid(),$1,$2,$3,$4,$5)`, claim.ConversationID, topicID, version, claim.MessageID, action); err != nil {
			return err
		}
	}
	if err := finishTopicRoute(ctx, tx, claim, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func routeSignals(segments []ob.Segment, botID int64) (reference int64, quoted, image bool, body string, addressedElsewhere, addressedBot bool) {
	count := 0
	var text strings.Builder
	for _, segment := range segments {
		switch segment.Type {
		case ob.SegReply:
			count++
			reference, _ = strconv.ParseInt(segment.Data["id"], 10, 64)
		case ob.SegAt:
			toBot := segment.Data["qq"] == strconv.FormatInt(botID, 10)
			addressedBot = addressedBot || toBot
			addressedElsewhere = addressedElsewhere || !toBot
		case ob.SegImage:
			image = true
		case ob.SegText:
			text.WriteString(segment.Data["text"])
		}
	}
	return reference, count == 1 && reference != 0, image, strings.TrimSpace(text.String()), addressedElsewhere, addressedBot
}

func matchesRequestedMaterial(request, body string, image bool) bool {
	for _, label := range []string{"图片", "截图", "发图", "张图"} {
		if strings.Contains(request, label) {
			return image
		}
	}
	for _, label := range []string{"文字", "文本", "日志", "代码"} {
		if !strings.Contains(request, label) {
			continue
		}
		if strings.HasPrefix(body, "```") && strings.Contains(body[3:], "\n") {
			return true
		}
		for _, prefix := range []string{label, "这是" + label} {
			if rest, ok := strings.CutPrefix(body, prefix); ok {
				rest = strings.TrimSpace(rest)
				if strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "：") {
					return strings.TrimSpace(strings.TrimLeft(rest, ":：")) != ""
				}
			}
		}
	}
	return false
}

func announcesTopicMaterial(body string) bool {
	if !strings.Contains(body, "发") {
		return false
	}
	var future bool
	for _, marker := range []string{"下面", "接下来", "等会", "待会", "稍后", "接着"} {
		if strings.Contains(body, marker) {
			future = true
			break
		}
	}
	if !future {
		return false
	}
	for _, marker := range []string{"图片", "截图", "发图", "张图", "文字", "文本", "日志", "代码"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func isDirectQuestion(body string) bool {
	return strings.HasSuffix(body, "？") || strings.HasSuffix(body, "?")
}

func isTopicCorrection(body string) bool {
	for _, prefix := range []string{"更正", "改口", "纠正", "我说错了", "刚才说错了", "改成"} {
		if strings.HasPrefix(body, prefix) {
			return true
		}
	}
	return false
}
