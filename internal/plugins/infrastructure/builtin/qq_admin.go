// Built-in group moderation tool: mute one member of the current group.
// The target group is always the current conversation and the bot never mutes itself.
// 内置群管理工具：禁言当前群的一位成员。
// 目标群固定为当前会话，机器人不会禁言自己。
package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// GroupBanner performs the mute call.
// GroupBanner 执行禁言调用。
type GroupBanner interface {
	SetGroupBan(ctx context.Context, groupID obdomain.GroupID, userID obdomain.UserID, durationSeconds int64) error
}

// AdminAccountReader resolves the moderation surface of one account.
// AdminAccountReader 解析单个账号的管理能力。
type AdminAccountReader interface {
	GroupBanner(botID int64) (GroupBanner, bool)
}

// adminProvider exposes group moderation tools.
// adminProvider 暴露群管理工具。
type adminProvider struct{ accounts AdminAccountReader }

// NewQQAdminProvider builds the provider.
// NewQQAdminProvider 构建提供方。
func NewQQAdminProvider(accounts AdminAccountReader) contracts.ToolProvider {
	return adminProvider{accounts: accounts}
}

// Manifest identifies the provider.
// Manifest 标识提供方。
func (adminProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.qq.admin", Version: "1", Description: "当前群的成员管理"}
}

// Close releases nothing.
// Close 不释放任何资源。
func (adminProvider) Close(context.Context) error { return nil }

// Tools returns the mute tool.
// Tools 返回禁言工具。
func (p adminProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{{
		Descriptor: contracts.ToolDescriptor{
			ID:          "qq.mute_group_member",
			Description: "在当前群禁言一位成员，duration_seconds 取 0（解除禁言）到 3600 秒；参数 user_id 为该成员 QQ 号",
			Category:    contracts.CategoryGroup,
			ScopeKinds:  []string{contracts.ScopeKindGroup},
			RiskLevel:   contracts.RiskWrite,
			ReadOnly:    false,
			SideEffect:  true,
			Permission:  "qq.group.admin.mute",
		},
		Invoke: p.mute,
	}}, nil
}

// muteArgs is the model-supplied payload.
// muteArgs 是模型提供的参数。
type muteArgs struct {
	UserID          int64 `json:"user_id"`
	DurationSeconds int64 `json:"duration_seconds"`
}

// maxMuteSeconds bounds one mute to an hour; longer bans must go through a human.
// maxMuteSeconds 把单次禁言限制在一小时内；更长的处罚交给人工处理。
const maxMuteSeconds = 3600

// mute applies one mute inside the current group.
// mute 在当前群执行一次禁言。
func (p adminProvider) mute(ctx context.Context, argumentsInJSON string) (string, error) {
	var args muteArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "error: 参数必须是 JSON 对象且包含 user_id 与 duration_seconds", nil
	}
	if args.UserID <= 0 {
		return "error: user_id 必须是正整数 QQ 号", nil
	}
	if args.DurationSeconds < 0 || args.DurationSeconds > maxMuteSeconds {
		return fmt.Sprintf("error: duration_seconds 必须在 0 到 %d 之间", maxMuteSeconds), nil
	}
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("qq admin tool: missing run scope")
	}
	if scope.Kind != contracts.ScopeKindGroup || scope.TargetID <= 0 {
		return "error: 只能在群聊会话中管理本群成员", nil
	}
	if args.UserID == scope.BotID {
		return "error: 不能对机器人自己执行禁言", nil
	}
	writer, ok := p.accounts.GroupBanner(scope.BotID)
	if !ok {
		return "error: 当前机器人账号不可用", nil
	}
	if err := writer.SetGroupBan(ctx, obdomain.GroupID(scope.TargetID), obdomain.UserID(args.UserID), args.DurationSeconds); err != nil {
		return "", fmt.Errorf("qq admin tool: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"user_id": args.UserID, "duration_seconds": args.DurationSeconds, "group_id": scope.TargetID,
	})
	if err != nil {
		return "", fmt.Errorf("qq admin tool: encode result: %w", err)
	}
	return string(payload), nil
}
