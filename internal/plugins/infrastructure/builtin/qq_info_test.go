// Tests for the read-only group info tool: it may only read the current group
// through the account that received the message.
// 只读群信息工具测试：只能通过接收消息的账号读取当前群。
package builtin

import (
	"context"
	"strings"
	"testing"

	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/QQBot/internal/plugins/application"
)

// fakeGroupReader records which group was queried.
// fakeGroupReader 记录被查询的群。
type fakeGroupReader struct {
	queried obdomain.GroupID
}

func (f *fakeGroupReader) GetGroupInfo(_ context.Context, id obdomain.GroupID, _ bool) (obdomain.GroupInfo, error) {
	f.queried = id
	return obdomain.GroupInfo{GroupID: id, GroupName: "测试群", MemberCount: 12, MaxMemberCount: 200}, nil
}

// fakeAccounts resolves readers per account and records the requested bot.
// fakeAccounts 按账号解析读取器并记录请求的 bot。
type fakeAccounts struct {
	reader fakeGroupReader
	botID  int64
	known  bool
}

func (f *fakeAccounts) GroupInfoReader(botID int64) (GroupInfoReader, bool) {
	f.botID = botID
	if !f.known {
		return nil, false
	}
	return &f.reader, true
}

func groupTool(t *testing.T, accounts AccountInfoReader) func(context.Context, string) (string, error) {
	t.Helper()
	registered, err := NewQQProvider(accounts).Tools(context.Background())
	if err != nil {
		t.Fatalf("tools: %v", err)
	}
	if len(registered) != 1 || registered[0].Descriptor.ID != "qq.get_group_info" {
		t.Fatalf("tools = %+v", registered)
	}
	return registered[0].Invoke
}

// TestGroupInfoUsesCurrentGroupOnly verifies the queried group always comes from
// the authorized scope, never from model arguments.
// TestGroupInfoUsesCurrentGroupOnly 验证查询目标始终来自授权作用域，
// 而不是模型参数。
func TestGroupInfoUsesCurrentGroupOnly(t *testing.T) {
	accounts := &fakeAccounts{known: true}
	invoke := groupTool(t, accounts)
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, Kind: "group", TargetID: 700,
	})

	out, err := invoke(ctx, `{"group_id": 999999}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if accounts.reader.queried != 700 {
		t.Fatalf("queried group = %d, want the scoped 700", accounts.reader.queried)
	}
	if accounts.botID != 100 {
		t.Fatalf("resolved bot = %d, want 100", accounts.botID)
	}
	if !strings.Contains(out, "测试群") || !strings.Contains(out, "12") {
		t.Fatalf("out = %q", out)
	}
}

// TestGroupInfoRejectsPrivateScope verifies private conversations cannot query
// group data.
// TestGroupInfoRejectsPrivateScope 验证私聊会话不能查询群数据。
func TestGroupInfoRejectsPrivateScope(t *testing.T) {
	accounts := &fakeAccounts{known: true}
	invoke := groupTool(t, accounts)
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, Kind: "private", TargetID: 200,
	})

	out, err := invoke(ctx, "")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, "只能在群聊") {
		t.Fatalf("out = %q", out)
	}
	if accounts.reader.queried != 0 {
		t.Fatal("private scope must not reach OneBot")
	}
}

// TestGroupInfoMissingAccount verifies an offline account degrades to text.
// TestGroupInfoMissingAccount 验证账号不可用时降级为文本。
func TestGroupInfoMissingAccount(t *testing.T) {
	invoke := groupTool(t, &fakeAccounts{known: false})
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, Kind: "group", TargetID: 700,
	})

	out, err := invoke(ctx, "")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, "不可用") {
		t.Fatalf("out = %q", out)
	}
}

// TestGroupInfoRequiresScope verifies running without a scope fails closed.
// TestGroupInfoRequiresScope 验证缺少作用域时关闭式失败。
func TestGroupInfoRequiresScope(t *testing.T) {
	invoke := groupTool(t, &fakeAccounts{known: true})
	if _, err := invoke(context.Background(), ""); err == nil {
		t.Fatal("missing scope must be an error")
	}
}
