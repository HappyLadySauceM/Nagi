// Built-in read-only member lookup bound to the current group.
// 绑定当前群的只读成员查询。
package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// MemberInfoReader is the narrow OneBot surface this tool needs.
// MemberInfoReader 是本工具需要的窄 OneBot 接口。
type MemberInfoReader interface {
	GetGroupMemberInfo(ctx context.Context, groupID obdomain.GroupID, userID obdomain.UserID, noCache bool) (obdomain.GroupMemberInfo, error)
}

// MemberAccountReader resolves the member reader of one account.
// MemberAccountReader 解析单个账号的成员读取器。
type MemberAccountReader interface {
	GroupMemberReader(botID int64) (MemberInfoReader, bool)
}

// memberProvider exposes the member lookup tool.
// memberProvider 暴露成员查询工具。
type memberProvider struct{ accounts MemberAccountReader }

// NewQQMemberProvider builds the provider; accounts must be non-nil.
// NewQQMemberProvider 构建提供方；accounts 不能为 nil。
func NewQQMemberProvider(accounts MemberAccountReader) contracts.ToolProvider {
	return memberProvider{accounts: accounts}
}

// Manifest identifies the provider.
// Manifest 标识提供方。
func (memberProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.qq.member", Version: "1", Description: "当前群成员信息"}
}

// Close releases nothing.
// Close 不释放任何资源。
func (memberProvider) Close(context.Context) error { return nil }

// Tools returns the member lookup tool.
// Tools 返回成员查询工具。
func (p memberProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{{
		Descriptor: contracts.ToolDescriptor{
			ID:          "qq.get_group_member_info",
			Description: "查询当前群某位成员的名片、昵称、角色与入群时间；参数 user_id 为该成员 QQ 号",
			Category:    contracts.CategoryGroup,
			ScopeKinds:  []string{contracts.ScopeKindGroup},
			RiskLevel:   contracts.RiskRead,
			ReadOnly:    true,
			Permission:  "qq.group.member.read",
		},
		Invoke: p.memberInfo,
	}}, nil
}

// memberArgs is the model-supplied payload.
// memberArgs 是模型提供的参数。
type memberArgs struct {
	UserID int64 `json:"user_id"`
}

// memberInfo reads one member of the current group through the receiving account.
// memberInfo 通过接收消息的账号读取当前群的一位成员。
func (p memberProvider) memberInfo(ctx context.Context, argumentsInJSON string) (string, error) {
	var args memberArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil || args.UserID <= 0 {
		return "error: 参数需要 user_id（正整数 QQ 号）", nil
	}
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("qq member tool: missing run scope")
	}
	if scope.Kind != contracts.ScopeKindGroup || scope.TargetID <= 0 {
		return "error: 只能在群聊会话中查询本群成员", nil
	}
	reader, ok := p.accounts.GroupMemberReader(scope.BotID)
	if !ok {
		return "error: 当前机器人账号不可用", nil
	}
	info, err := reader.GetGroupMemberInfo(ctx, obdomain.GroupID(scope.TargetID), obdomain.UserID(args.UserID), false)
	if err != nil {
		return "", fmt.Errorf("qq member tool: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"user_id": info.UserID, "nickname": info.Nickname, "card": info.Card,
		"role": string(info.Role), "join_time": info.JoinTime,
	})
	if err != nil {
		return "", fmt.Errorf("qq member tool: encode result: %w", err)
	}
	return string(payload), nil
}
