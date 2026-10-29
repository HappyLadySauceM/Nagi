// Built-in OneBot query tool. It never accepts a group id from the model: the
// target always comes from the authorized conversation scope.
// 内置 OneBot 查询工具。它绝不接受模型传入的群号：目标始终来自已授权会话作用域。
package builtin

import (
	"context"
	"fmt"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// GroupInfoReader is the narrow OneBot query surface this tool needs.
// GroupInfoReader 是本工具需要的窄 OneBot 查询接口。
type GroupInfoReader interface {
	GetGroupInfo(ctx context.Context, id obdomain.GroupID, noCache bool) (obdomain.GroupInfo, error)
}

// AccountInfoReader resolves the group-info reader of one account.
// AccountInfoReader 解析单个账号的群信息读取器。
type AccountInfoReader interface {
	GroupInfoReader(botID int64) (GroupInfoReader, bool)
}

// qqProvider exposes read-only QQ queries bound to the current conversation.
// qqProvider 暴露绑定当前会话的只读 QQ 查询。
type qqProvider struct {
	accounts AccountInfoReader
}

// NewQQProvider builds the provider; accounts must be non-nil.
// NewQQProvider 构建提供方；accounts 不能为 nil。
func NewQQProvider(accounts AccountInfoReader) contracts.ToolProvider {
	return qqProvider{accounts: accounts}
}

// Manifest identifies the provider.
// Manifest 标识提供方。
func (qqProvider) Manifest() contracts.Manifest {
	return contracts.Manifest{ID: "builtin.qq", Version: "1", Description: "当前账号与当前群的只读信息"}
}

// Tools returns the group-info tool.
// Tools 返回群信息工具。
func (p qqProvider) Tools(context.Context) ([]contracts.RegisteredTool, error) {
	return []contracts.RegisteredTool{{
		Descriptor: contracts.ToolDescriptor{
			ID:          "qq.get_group_info",
			Description: "查询当前群的基本信息（群号、群名、成员数、上限），不需要参数",
			Category:    contracts.CategoryGroup,
			// Group-only: private runs must never see this tool.
			// 仅群聊可用：私聊运行不得看到该工具。
			ScopeKinds: []string{contracts.ScopeKindGroup},
			RiskLevel:  contracts.RiskRead,
			ReadOnly:   true,
			Permission: "qq.group.read",
		},
		Invoke: p.groupInfo,
	}}, nil
}

// groupInfo reads the current group through the account that received the
// message; cross-group and cross-account queries are impossible by design.
// groupInfo 通过接收消息的账号读取当前群；跨群与跨账号查询在结构上不可能。
func (p qqProvider) groupInfo(ctx context.Context, _ string) (string, error) {
	scope, ok := pluginsapp.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("qq tool: missing run scope")
	}
	if scope.Kind != contracts.ScopeKindGroup || scope.TargetID <= 0 {
		return "error: 只能在群聊会话中查询本群信息", nil
	}
	reader, ok := p.accounts.GroupInfoReader(scope.BotID)
	if !ok {
		return "error: 当前机器人账号不可用", nil
	}
	info, err := reader.GetGroupInfo(ctx, obdomain.GroupID(scope.TargetID), false)
	if err != nil {
		return "", fmt.Errorf("qq tool: get group info: %w", err)
	}
	return fmt.Sprintf("群号: %d\n群名: %s\n成员数: %d\n人数上限: %d",
		int64(info.GroupID), info.GroupName, info.MemberCount, info.MaxMemberCount), nil
}

// Close is a no-op: the account registry owns the connection.
// Close 是空操作：连接由账号注册表持有。
func (qqProvider) Close(context.Context) error { return nil }
