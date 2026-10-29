// Built-in group profile tools: change a member card or kick a member.
// 内置群资料工具：修改成员名片或移出群聊。
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// GroupProfiler is the narrow moderation surface these tools need.
// GroupProfiler 是这些工具所需的窄管理接口。
type GroupProfiler interface {
	SetGroupCard(ctx context.Context, groupID obdomain.GroupID, userID obdomain.UserID, card string) error
	SetGroupKick(ctx context.Context, groupID obdomain.GroupID, userID obdomain.UserID, rejectAddRequest bool) error
}

// ProfileAccountReader resolves the profiler of one account.
// ProfileAccountReader 解析单个账号的资料管理能力。
type ProfileAccountReader interface {
	GroupProfiler(botID int64) (GroupProfiler, bool)
}

// profileProvider exposes the card and kick tools.
// profileProvider 暴露名片与踢人工具。
type profileProvider struct{ accounts ProfileAccountReader }

// NewQQProfileProvider builds the provider.
// NewQQProfileProvider 构建提供方。
func NewQQProfileProvider(accounts ProfileAccountReader) contracts.ToolProvider {
	return profileProvider{accounts: accounts}
}

// Manifest identifies the provider.
// Manifest 标识提供方。
func (profileProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.qq.profile", Version: "1", Description: "当前群成员名片与移出"}
}

// Close releases nothing.
// Close 不释放任何资源。
func (profileProvider) Close(context.Context) error { return nil }

// Tools returns the card and kick tools; both are write tools and stay hidden
// until side effects are allowed and the scope grants their permission.
// Tools 返回名片与踢人工具；两者都是写工具，只有允许副作用并授予权限后才暴露。
func (p profileProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{
		{
			Descriptor: contracts.ToolDescriptor{
				ID:          "qq.set_group_card",
				Description: "修改当前群某位成员的群名片；参数 user_id 与 card（传空串表示清除名片）",
				Category:    contracts.CategoryGroup,
				ScopeKinds:  []string{contracts.ScopeKindGroup},
				RiskLevel:   contracts.RiskWrite,
				ReadOnly:    false,
				SideEffect:  true,
				Permission:  "qq.group.admin.card",
			},
			Invoke: p.setCard,
		},
		{
			Descriptor: contracts.ToolDescriptor{
				ID:          "qq.kick_group_member",
				Description: "把当前群某位成员移出群聊；参数 user_id。这是不可撤销操作，仅在明确要求时使用",
				Category:    contracts.CategoryGroup,
				ScopeKinds:  []string{contracts.ScopeKindGroup},
				RiskLevel:   contracts.RiskWrite,
				ReadOnly:    false,
				SideEffect:  true,
				Permission:  "qq.group.admin.kick",
			},
			Invoke: p.kick,
		},
	}, nil
}

// cardArgs is the model-supplied payload for the card tool.
// cardArgs 是名片工具的模型参数。
type cardArgs struct {
	UserID int64  `json:"user_id"`
	Card   string `json:"card"`
}

// maxCardRunes bounds a group card so the model cannot paste paragraphs.
// maxCardRunes 限制群名片长度，避免模型粘贴整段文本。
const maxCardRunes = 30

// setCard changes one member's group card.
// setCard 修改一位成员的群名片。
func (p profileProvider) setCard(ctx context.Context, argumentsInJSON string) (string, error) {
	var args cardArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil || args.UserID <= 0 {
		return "error: 参数需要 user_id（正整数 QQ 号）与 card", nil
	}
	card := strings.TrimSpace(args.Card)
	if len([]rune(card)) > maxCardRunes {
		return fmt.Sprintf("error: 群名片最多 %d 个字符", maxCardRunes), nil
	}
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("qq profile tool: missing run scope")
	}
	if scope.Kind != contracts.ScopeKindGroup || scope.TargetID <= 0 {
		return "error: 只能在群聊会话中修改本群名片", nil
	}
	writer, ok := p.accounts.GroupProfiler(scope.BotID)
	if !ok {
		return "error: 当前机器人账号不可用", nil
	}
	if err := writer.SetGroupCard(ctx, obdomain.GroupID(scope.TargetID), obdomain.UserID(args.UserID), card); err != nil {
		return "", fmt.Errorf("qq profile tool: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"user_id": args.UserID, "card": card})
	return string(payload), nil
}

// kickArgs is the model-supplied payload for the kick tool.
// kickArgs 是踢人工具的模型参数。
type kickArgs struct {
	UserID int64 `json:"user_id"`
}

// kick removes one member from the current group and blocks re-joining by request.
// kick 把一位成员移出当前群，并拒绝其重新申请。
func (p profileProvider) kick(ctx context.Context, argumentsInJSON string) (string, error) {
	var args kickArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil || args.UserID <= 0 {
		return "error: 参数需要 user_id（正整数 QQ 号）", nil
	}
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("qq profile tool: missing run scope")
	}
	if scope.Kind != contracts.ScopeKindGroup || scope.TargetID <= 0 {
		return "error: 只能在群聊会话中移出本群成员", nil
	}
	if args.UserID == scope.BotID {
		return "error: 不能移出机器人自己", nil
	}
	writer, ok := p.accounts.GroupProfiler(scope.BotID)
	if !ok {
		return "error: 当前机器人账号不可用", nil
	}
	if err := writer.SetGroupKick(ctx, obdomain.GroupID(scope.TargetID), obdomain.UserID(args.UserID), true); err != nil {
		return "", fmt.Errorf("qq profile tool: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"user_id": args.UserID, "group_id": scope.TargetID, "kicked": true})
	return string(payload), nil
}
