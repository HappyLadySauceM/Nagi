// Package contracts publishes the stable interfaces of the onebot context.
// Package contracts 发布 onebot 上下文对外协作的稳定接口。
// onebot/domain 是 OneBot v11 协议的“已发布语言”，业务上下文经本包引用协议值类型属于允许的例外。
package contracts

import (
	"context"
	"errors"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// ErrActionTimeout reports that an action call timed out without an echo-matched
// response. The real-world outcome (whether the message was delivered) is
// unknown, so callers must not blindly retry sends.
// ErrActionTimeout 表示动作调用在期限内未收到 echo 匹配的响应。
// 实际结果（消息是否已发出）未知，调用方不得盲目重试发送。
var ErrActionTimeout = errors.New("onebot: action response timed out")

// Sender is the stable action-sending interface for business contexts.
// Sender 是业务上下文发送 OneBot 动作的稳定接口。
type Sender interface {
	SendPrivateMessage(ctx context.Context, to domain.UserID, segments ...domain.Segment) error
	SendGroupMessage(ctx context.Context, to domain.GroupID, segments ...domain.Segment) error
	DeleteMessage(ctx context.Context, id domain.MessageID) error
}

// Messenger covers message sending, fetching, likes and merged forwards.
// Messenger 覆盖消息收发、获取、点赞与合并转发。
type Messenger interface {
	Sender
	// SendMessage sends with an explicit message type and target.
	// SendMessage 以显式消息类型发送（send_msg）；target 按类型为用户或群 ID。
	SendMessage(ctx context.Context, mt domain.MessageType, target int64, segments ...domain.Segment) error
	// SendLike sends likes to a friend; times is clamped into 1..10.
	// SendLike 给好友点赞；次数收敛到 1..10。
	SendLike(ctx context.Context, to domain.UserID, times int) error
	// GetMessage fetches one message by id.
	// GetMessage 按消息 ID 获取消息。
	GetMessage(ctx context.Context, id domain.MessageID) (domain.MessageResult, error)
	// GetForwardMessages fetches merged forward content by its string resource id.
	// GetForwardMessages 按字符串资源 ID 获取合并转发内容。
	GetForwardMessages(ctx context.Context, id string) ([]domain.ForwardNode, error)
	// SendPrivateForward sends merged forward nodes in private chat.
	// SendPrivateForward 在私聊发送合并转发节点。
	SendPrivateForward(ctx context.Context, to domain.UserID, nodes ...domain.Segment) (domain.ForwardReceipt, error)
	// SendGroupForward sends merged forward nodes in group chat.
	// SendGroupForward 在群聊发送合并转发节点。
	SendGroupForward(ctx context.Context, to domain.GroupID, nodes ...domain.Segment) (domain.ForwardReceipt, error)
}

// GroupAdmin covers group administration actions.
// GroupAdmin 覆盖群管理动作。
type GroupAdmin interface {
	SetGroupKick(ctx context.Context, groupID domain.GroupID, userID domain.UserID, rejectAddRequest bool) error
	SetGroupBan(ctx context.Context, groupID domain.GroupID, userID domain.UserID, durationSeconds int64) error
	SetGroupAnonymousBan(ctx context.Context, groupID domain.GroupID, flag string, durationSeconds int64) error
	SetGroupWholeBan(ctx context.Context, groupID domain.GroupID, enable bool) error
	SetGroupAdmin(ctx context.Context, groupID domain.GroupID, userID domain.UserID, enable bool) error
	SetGroupAnonymous(ctx context.Context, groupID domain.GroupID, enable bool) error
	SetGroupCard(ctx context.Context, groupID domain.GroupID, userID domain.UserID, card string) error
	SetGroupName(ctx context.Context, groupID domain.GroupID, name string) error
	SetGroupLeave(ctx context.Context, groupID domain.GroupID, dismiss bool) error
	SetGroupSpecialTitle(ctx context.Context, groupID domain.GroupID, userID domain.UserID, title string) error
}

// InfoReader covers account, group, member, media and capability queries.
// InfoReader 覆盖账号、群、成员、媒体与能力查询。
type InfoReader interface {
	GetLoginInfo(ctx context.Context) (domain.LoginInfo, error)
	GetStrangerInfo(ctx context.Context, id domain.UserID, noCache bool) (domain.StrangerInfo, error)
	GetFriendList(ctx context.Context) ([]domain.FriendInfo, error)
	GetGroupInfo(ctx context.Context, id domain.GroupID, noCache bool) (domain.GroupInfo, error)
	GetGroupList(ctx context.Context, noCache bool) ([]domain.GroupInfo, error)
	GetGroupMemberInfo(ctx context.Context, groupID domain.GroupID, userID domain.UserID, noCache bool) (domain.GroupMemberInfo, error)
	GetGroupMemberList(ctx context.Context, groupID domain.GroupID) ([]domain.GroupMemberInfo, error)
	GetGroupHonorInfo(ctx context.Context, groupID domain.GroupID, honorType string) (domain.HonorInfo, error)
	GetCookies(ctx context.Context, domainName string) (domain.CookiesResult, error)
	GetCSRFToken(ctx context.Context) (domain.CSRFTokenResult, error)
	GetCredentials(ctx context.Context, domainName string) (domain.CredentialsResult, error)
	GetRecord(ctx context.Context, file, outFormat string) (domain.FileResult, error)
	GetImage(ctx context.Context, file string) (domain.FileResult, error)
	CanSendImage(ctx context.Context) (domain.BoolCapability, error)
	CanSendRecord(ctx context.Context) (domain.BoolCapability, error)
	GetRuntimeStatus(ctx context.Context) (domain.RuntimeStatus, error)
	GetVersionInfo(ctx context.Context) (domain.VersionInfo, error)
}

// RequestApprover covers friend and group request approvals.
// RequestApprover 覆盖好友与加群请求审批。
type RequestApprover interface {
	SetFriendAddRequest(ctx context.Context, flag string, approve bool, remark string) error
	SetGroupAddRequest(ctx context.Context, flag string, subType domain.GroupRequestSubType, approve bool, reason string) error
}

// Maintenance covers restart and cache maintenance.
// Maintenance 覆盖重启与缓存维护。
type Maintenance interface {
	SetRestart(ctx context.Context, delayMillis int64) error
	CleanCache(ctx context.Context) error
}

// API aggregates all action capabilities of the onebot context.
// API 聚合 onebot 上下文的全部动作能力。
type API interface {
	Messenger
	GroupAdmin
	InfoReader
	RequestApprover
	Maintenance
}

// EventSource is the stable event-reading interface for business contexts.
// EventSource 是业务上下文读取 OneBot 事件的稳定接口。
type EventSource interface {
	Events() <-chan domain.Event
	Connected() bool
}

// BotStatus is a read-only snapshot of the connection state.
// BotStatus 是连接状态的只读快照。
type BotStatus struct {
	Connected   bool
	SelfID      domain.BotID
	SelfIDKnown bool

	// Heartbeat visibility derived from meta_event frames.
	// 由 meta_event 帧推导的心跳可见性。
	HeartbeatSeen           bool
	LastHeartbeatAt         time.Time
	HeartbeatIntervalMillis int64
}

// StatusReader queries the current connection snapshot.
// StatusReader 查询当前连接状态快照。
type StatusReader interface {
	Status() BotStatus
}

// PublicAPI executes the closed set of standard public commands.
// PublicAPI 执行标准公开命令的封闭集合。
type PublicAPI interface {
	ExecutePublic(context.Context, domain.PublicCommand) (domain.PublicResult, error)
}
