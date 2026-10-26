// Membership handler keeps the group directory current from notice events.
// Membership 处理器用通知事件保持群成员目录最新。
package infrastructure

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	botcontracts "github.com/HappyLadySauceM/Nagi/internal/bot/contracts"
	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// MembershipApplier is the subset of the directory service needed here.
// MembershipApplier 是本处理器需要的目录服务子集。
type MembershipApplier interface {
	ApplyMemberJoin(ctx context.Context, botID, groupID, userID int64) error
	ApplyMemberLeave(ctx context.Context, botID, groupID, userID int64) error
	ApplyBotJoined(ctx context.Context, botID, groupID int64) error
	ApplyBotLeft(ctx context.Context, botID, groupID int64) error
}

// MembershipHandler applies group increase/decrease notices to the directory.
// MembershipHandler 把入群/退群通知应用到目录数据。
type MembershipHandler struct {
	directory MembershipApplier
	logger    *slog.Logger
}

// NewMembershipHandler validates dependencies and builds the handler.
// NewMembershipHandler 校验依赖并构建处理器。
func NewMembershipHandler(directory MembershipApplier, logger *slog.Logger) (*MembershipHandler, error) {
	if directory == nil || logger == nil {
		return nil, fmt.Errorf("directory: membership dependencies required")
	}
	return &MembershipHandler{directory: directory, logger: logger}, nil
}

// Name identifies the handler in logs and status output.
// Name 标识处理器，用于日志与状态输出。
func (h *MembershipHandler) Name() string { return "directory_membership" }

// Match accepts group increase/decrease notices only; it must stay cheap.
// Match 仅接受入群/退群通知；必须保持廉价。
func (h *MembershipHandler) Match(event obdomain.Event) bool {
	if event.Post != obdomain.PostNotice || int64(event.GroupID) <= 0 {
		return false
	}
	switch event.NoticeType() {
	case obdomain.NoticeGroupIncrease, obdomain.NoticeGroupDecrease:
		return true
	default:
		return false
	}
}

// Handle detaches the OneBot round-trips from the dispatcher: the notice is
// applied in a goroutine so a slow group query never blocks the event loop.
// Handle 将 OneBot 往返调用与分发器解耦：通知在独立协程中处理，
// 慢查询不会阻塞事件循环。
func (h *MembershipHandler) Handle(ctx context.Context, botID obdomain.BotID, event obdomain.Event) error {
	// The dispatcher owns the verified account binding; the frame's self_id is
	// only a hint and must never decide where rows are written.
	// 账号绑定由分发器保证；帧内的 self_id 只是提示，绝不能决定写入哪个账号。
	account := int64(botID)
	if account <= 0 {
		return nil
	}
	asyncCtx := context.WithoutCancel(ctx)
	go h.apply(asyncCtx, account, event)
	return nil
}

// apply performs the membership change and only logs failures.
// apply 执行成员变更，失败时只记录日志。
func (h *MembershipHandler) apply(ctx context.Context, botID int64, event obdomain.Event) {
	groupID := int64(event.GroupID)
	userID := int64(event.UserID)
	subType := strings.ToLower(event.SubType)

	var err error
	switch event.NoticeType() {
	case obdomain.NoticeGroupIncrease:
		if userID == botID {
			err = h.directory.ApplyBotJoined(ctx, botID, groupID)
		} else {
			err = h.directory.ApplyMemberJoin(ctx, botID, groupID, userID)
		}
	case obdomain.NoticeGroupDecrease:
		if subType == "kick_me" || userID == botID {
			err = h.directory.ApplyBotLeft(ctx, botID, groupID)
		} else {
			err = h.directory.ApplyMemberLeave(ctx, botID, groupID, userID)
		}
	}
	if err != nil {
		h.logger.Warn("directory membership update failed",
			"bot_id", botID, "group_id", groupID, "user_id", userID,
			"notice", string(event.NoticeType()), "error", err)
	}
}

var _ botcontracts.Handler = (*MembershipHandler)(nil)
