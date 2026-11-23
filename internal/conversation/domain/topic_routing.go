package domain

import (
	"errors"
	"time"
)

var ErrTopicRouteClaimLost = errors.New("conversation: topic route claim lost")

// TopicRouteClaim is the fenced lease for one inbound message and its immutable candidate snapshot.
// TopicRouteClaim 表示一条入站消息的受令牌保护的租约及其不可变候选快照。
type TopicRouteClaim struct {
	MessageID      int64
	ConversationID string
	Token          string
	Deadline       time.Time
	LeaseExpiresAt time.Time
	Candidates     []TopicRouteCandidate
}

type TopicRouteCandidate struct {
	TopicID int64
	Version int64
}

type TopicRouteAction string

const (
	TopicRouteUnrelated    TopicRouteAction = "unrelated"
	TopicRouteUnavailable  TopicRouteAction = "unavailable"
	TopicRouteOpen         TopicRouteAction = "opened"
	TopicRouteWaiting      TopicRouteAction = "waiting"
	TopicRouteFollowUp     TopicRouteAction = "follow_up"
	TopicRouteContribution TopicRouteAction = "contribution"
	TopicRouteCorrection   TopicRouteAction = "corrected"
	TopicRouteReplacement  TopicRouteAction = "replaced"
	TopicRouteMerge        TopicRouteAction = "merged"
	TopicRouteClose        TopicRouteAction = "close"
)

// TopicRouteResolution carries a trusted classifier result. TopicID is zero when opening a topic or releasing every candidate.
// TopicRouteResolution 携带可信归属结果。开题或解除所有候选时 TopicID 为零。
type TopicRouteResolution struct {
	Action    TopicRouteAction
	TopicID   int64
	ExpiresAt time.Time
}

type TopicRouteResult struct {
	Status  string
	TopicID int64
	Version int64
}
