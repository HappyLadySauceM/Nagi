// Tests for the group mute tool.
// 群禁言工具的测试。
package builtin

import (
	"context"
	"errors"
	"strings"
	"testing"

	obdomain "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pluginsapp "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type banWriter struct {
	group    int64
	user     int64
	duration int64
	err      error
	called   bool
}

func (b *banWriter) SetGroupBan(_ context.Context, groupID obdomain.GroupID, userID obdomain.UserID, durationSeconds int64) error {
	b.called = true
	b.group, b.user, b.duration = int64(groupID), int64(userID), durationSeconds
	return b.err
}

type banAccounts struct{ writer *banWriter }

func (a banAccounts) GroupBanner(int64) (GroupBanner, bool) { return a.writer, true }

func muteTool(t *testing.T, writer *banWriter) contracts.RegisteredTool {
	t.Helper()
	tools, err := NewQQAdminProvider(banAccounts{writer: writer}).Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %d", err, len(tools))
	}
	return tools[0]
}

func groupScope() context.Context {
	return pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, JobID: 1, Kind: contracts.ScopeKindGroup, TargetID: 700,
	})
}

func TestMuteToolBansMemberInCurrentGroup(t *testing.T) {
	writer := &banWriter{}
	out, err := muteTool(t, writer).Invoke(groupScope(), `{"user_id":200,"duration_seconds":600}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !writer.called || writer.group != 700 || writer.user != 200 || writer.duration != 600 {
		t.Fatalf("writer = %+v", writer)
	}
	if !strings.Contains(out, `"duration_seconds":600`) {
		t.Fatalf("out = %s", out)
	}
}

func TestMuteToolRejectsSelfAndOutOfRangeAndPrivate(t *testing.T) {
	writer := &banWriter{}
	tool := muteTool(t, writer)
	cases := []struct{ args, want string }{
		{`{"user_id":100,"duration_seconds":60}`, "不能对机器人自己"},
		{`{"user_id":200,"duration_seconds":7200}`, "duration_seconds"},
		{`{"user_id":0,"duration_seconds":60}`, "user_id"},
	}
	for _, tc := range cases {
		out, err := tool.Invoke(groupScope(), tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.args, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s => %s, want %s", tc.args, out, tc.want)
		}
	}
	ctx := pluginsapp.WithScope(context.Background(), pluginsapp.Scope{
		BotID: 100, JobID: 1, Kind: contracts.ScopeKindPrivate, TargetID: 200,
	})
	if out, _ := tool.Invoke(ctx, `{"user_id":300,"duration_seconds":60}`); !strings.Contains(out, "只能在群聊") {
		t.Fatalf("private scope out = %s", out)
	}
	if writer.called {
		t.Fatalf("rejected calls must not reach OneBot")
	}
}

func TestMuteToolPropagatesOneBotError(t *testing.T) {
	writer := &banWriter{err: errors.New("no permission")}
	if _, err := muteTool(t, writer).Invoke(groupScope(), `{"user_id":200,"duration_seconds":60}`); err == nil {
		t.Fatal("expected the OneBot error to surface")
	}
}
