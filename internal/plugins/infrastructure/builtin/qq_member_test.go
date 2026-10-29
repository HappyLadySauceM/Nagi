// Tests for the group member tool.
// 群成员工具的测试。
package builtin

import (
	"context"
	"strings"
	"testing"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type memberReader struct{ called bool }

func (m *memberReader) GetGroupMemberInfo(_ context.Context, groupID obdomain.GroupID, userID obdomain.UserID, _ bool) (obdomain.GroupMemberInfo, error) {
	m.called = true
	return obdomain.GroupMemberInfo{GroupID: groupID, UserID: userID, Nickname: "小明", Role: obdomain.RoleMember}, nil
}

type memberAccounts struct{ reader *memberReader }

func (a memberAccounts) GroupMemberReader(int64) (MemberInfoReader, bool) { return a.reader, true }

func TestMemberToolReadsCurrentGroupMember(t *testing.T) {
	reader := &memberReader{}
	tools, err := NewQQMemberProvider(memberAccounts{reader: reader}).Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %d", err, len(tools))
	}
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 1, JobID: 1, Kind: contracts.ScopeKindGroup, TargetID: 700,
	})
	out, err := tools[0].Invoke(ctx, `{"user_id":12345}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !reader.called || !strings.Contains(out, "小明") || !strings.Contains(out, "12345") {
		t.Fatalf("out = %s called=%v", out, reader.called)
	}
}

func TestMemberToolRejectsPrivateScope(t *testing.T) {
	reader := &memberReader{}
	tools, _ := NewQQMemberProvider(memberAccounts{reader: reader}).Tools(context.Background())
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 1, JobID: 1, Kind: contracts.ScopeKindPrivate, TargetID: 700,
	})
	out, err := tools[0].Invoke(ctx, `{"user_id":12345}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !strings.Contains(out, "只能在群聊") || reader.called {
		t.Fatalf("out = %s called=%v", out, reader.called)
	}
}
