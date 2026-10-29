// Tests for the card and kick tools.
// 名片与踢人工具的测试。
package builtin

import (
	"context"
	"strings"
	"testing"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type profileWriter struct {
	card   string
	kicked int64
	reject bool
	called bool
	left   bool
}

func (p *profileWriter) SetGroupCard(_ context.Context, _ obdomain.GroupID, _ obdomain.UserID, card string) error {
	p.called = true
	p.card = card
	return nil
}

func (p *profileWriter) SetGroupLeave(_ context.Context, _ obdomain.GroupID, dismiss bool) error {
	p.called = true
	p.left = !dismiss
	return nil
}

func (p *profileWriter) SetGroupKick(_ context.Context, _ obdomain.GroupID, user obdomain.UserID, reject bool) error {
	p.called = true
	p.kicked = int64(user)
	p.reject = reject
	return nil
}

type profileAccounts struct{ writer *profileWriter }

func (a profileAccounts) GroupProfiler(int64) (GroupProfiler, bool) { return a.writer, true }

func profileTools(t *testing.T, writer *profileWriter) map[string]contracts.RegisteredTool {
	t.Helper()
	tools, err := NewQQProfileProvider(profileAccounts{writer: writer}).Tools(context.Background())
	if err != nil || len(tools) != 3 {
		t.Fatalf("tools: %v %d", err, len(tools))
	}
	out := map[string]contracts.RegisteredTool{}
	for _, tool := range tools {
		out[tool.Descriptor.ID] = tool
	}
	return out
}

func TestCardToolSetsTrimmedCard(t *testing.T) {
	writer := &profileWriter{}
	tools := profileTools(t, writer)
	out, err := tools["qq.set_group_card"].Invoke(groupScope(), `{"user_id":200,"card":"  小明  "}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if writer.card != "小明" || !strings.Contains(out, "小明") {
		t.Fatalf("card = %q out = %s", writer.card, out)
	}
}

func TestCardToolRejectsLongCardAndPrivateScope(t *testing.T) {
	writer := &profileWriter{}
	tools := profileTools(t, writer)
	long := strings.Repeat("字", 31)
	if out, _ := tools["qq.set_group_card"].Invoke(groupScope(), `{"user_id":200,"card":"`+long+`"}`); !strings.Contains(out, "最多") {
		t.Fatalf("long card out = %s", out)
	}
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, JobID: 1, Kind: contracts.ScopeKindPrivate, TargetID: 200,
	})
	if out, _ := tools["qq.set_group_card"].Invoke(ctx, `{"user_id":200,"card":"x"}`); !strings.Contains(out, "只能在群聊") {
		t.Fatalf("private out = %s", out)
	}
	if writer.called {
		t.Fatal("rejected calls must not reach OneBot")
	}
}

func TestKickToolKicksAndRejectsSelf(t *testing.T) {
	writer := &profileWriter{}
	tools := profileTools(t, writer)
	if _, err := tools["qq.kick_group_member"].Invoke(groupScope(), `{"user_id":300}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if writer.kicked != 300 || !writer.reject {
		t.Fatalf("kick = %+v", writer)
	}
	if out, _ := tools["qq.kick_group_member"].Invoke(groupScope(), `{"user_id":100}`); !strings.Contains(out, "不能移出机器人") {
		t.Fatalf("self kick out = %s", out)
	}
}

// TestLeaveToolLeavesWithoutDissolving verifies the bot leaves without dissolving.
// TestLeaveToolLeavesWithoutDissolving 验证机器人退群且不解散群。
func TestLeaveToolLeavesWithoutDissolving(t *testing.T) {
	writer := &profileWriter{}
	tools := profileTools(t, writer)
	if _, err := tools["qq.leave_current_group"].Invoke(groupScope(), `{}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !writer.left {
		t.Fatal("leave was not dispatched")
	}
}
