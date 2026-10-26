// Tests for the server-side tool guard.
// 服务端工具守卫的测试。
package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

func readOnly(id, permission string) contracts.ToolDescriptor {
	return contracts.ToolDescriptor{ID: id, Description: "test", ReadOnly: true, Permission: permission}
}

func testScope() Scope {
	return Scope{
		BotID: 100, ConversationID: "conv-1", Kind: "group", TargetID: 700, SenderID: 200,
		Permissions: map[string]bool{"qq.group.read": true},
		Timeout:     time.Second, MaxResultBytes: 16,
	}
}

// TestGuardAllowsGrantedTool verifies the happy path and scope propagation.
// TestGuardAllowsGrantedTool 验证放行路径与作用域传递。
func TestGuardAllowsGrantedTool(t *testing.T) {
	var seen Scope
	invoke := Guard(testScope(), readOnly("qq.get_group_info", "qq.group.read"),
		func(ctx context.Context, _ string) (string, error) {
			var ok bool
			seen, ok = ScopeFrom(ctx)
			if !ok {
				t.Fatal("tool must receive the run scope")
			}
			return "群号: 700", nil
		})
	out, err := invoke(context.Background(), "{}")
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if out != "群号: 700" {
		t.Fatalf("out = %q", out)
	}
	if seen.BotID != 100 || seen.TargetID != 700 {
		t.Fatalf("scope = %+v", seen)
	}
}

// TestGuardReportsToolRun verifies every terminal outcome reaches the run
// recorder, including denials that never execute the tool.
// TestGuardReportsToolRun 验证每种终态都会上报给运行记录器，
// 包括未执行工具本身的拒绝路径。
func TestGuardReportsToolRun(t *testing.T) {
	var runs []contracts.ToolRun
	scope := testScope()
	scope.Record = func(run contracts.ToolRun) { runs = append(runs, run) }

	allowed := Guard(scope, readOnly("qq.get_group_info", "qq.group.read"),
		func(context.Context, string) (string, error) { return "ok", nil })
	if _, err := allowed(context.Background(), "{}"); err != nil {
		t.Fatalf("allowed invoke: %v", err)
	}

	denied := Guard(scope, readOnly("qq.get_group_info", "qq.admin"),
		func(context.Context, string) (string, error) {
			t.Fatal("denied tool must not execute")
			return "", nil
		})
	if _, err := denied(context.Background(), "{}"); err != nil {
		t.Fatalf("denied invoke: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("reported runs = %+v", runs)
	}
	if runs[0].ToolID != "qq.get_group_info" || runs[0].Status != StatusOK {
		t.Fatalf("first run = %+v", runs[0])
	}
	if runs[1].Status != StatusDenied {
		t.Fatalf("denied run = %+v", runs[1])
	}
}

// TestGuardDeniesMissingPermission verifies authorization happens before the
// tool runs.
// TestGuardDeniesMissingPermission 验证授权先于工具执行。
func TestGuardDeniesMissingPermission(t *testing.T) {
	called := false
	invoke := Guard(testScope(), readOnly("qq.get_group_info", "qq.admin"),
		func(context.Context, string) (string, error) {
			called = true
			return "", nil
		})
	out, err := invoke(context.Background(), "{}")
	if err != nil {
		t.Fatalf("denial must not fail the run: %v", err)
	}
	if !strings.Contains(out, "permission denied") {
		t.Fatalf("out = %q", out)
	}
	if called {
		t.Fatal("tool must not run without permission")
	}
}

// TestGuardDeniesNonReadOnly verifies write tools stay disabled.
// TestGuardDeniesNonReadOnly 验证写工具默认不开放。
func TestGuardDeniesNonReadOnly(t *testing.T) {
	descriptor := contracts.ToolDescriptor{ID: "qq.send_message", Description: "x", ReadOnly: false}
	out, _ := Guard(testScope(), descriptor, func(context.Context, string) (string, error) {
		t.Fatal("non read-only tool must not run")
		return "", nil
	})(context.Background(), "{}")
	if !strings.Contains(out, "not available") {
		t.Fatalf("out = %q", out)
	}
}

// TestGuardTimesOut verifies the per-call deadline is enforced.
// TestGuardTimesOut 验证单次调用超时生效。
func TestGuardTimesOut(t *testing.T) {
	scope := testScope()
	scope.Timeout = 20 * time.Millisecond
	invoke := Guard(scope, readOnly("system.current_time", ""),
		func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		})
	out, err := invoke(context.Background(), "{}")
	if err != nil {
		t.Fatalf("timeout must degrade to text: %v", err)
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("out = %q", out)
	}
}

// TestGuardRejectsOversizedResult verifies the result size bound.
// TestGuardRejectsOversizedResult 验证结果大小上限。
func TestGuardRejectsOversizedResult(t *testing.T) {
	invoke := Guard(testScope(), readOnly("system.current_time", ""),
		func(context.Context, string) (string, error) {
			return strings.Repeat("x", 64), nil
		})
	out, err := invoke(context.Background(), "{}")
	if err != nil {
		t.Fatalf("oversize must degrade to text: %v", err)
	}
	if !strings.Contains(out, "too large") {
		t.Fatalf("out = %q", out)
	}
}

// TestGuardReportsToolFailure verifies errors are not leaked verbatim.
// TestGuardReportsToolFailure 验证工具错误不会原样泄露给模型。
func TestGuardReportsToolFailure(t *testing.T) {
	invoke := Guard(testScope(), readOnly("system.current_time", ""),
		func(context.Context, string) (string, error) {
			return "", errors.New("db dsn=postgres://secret")
		})
	out, err := invoke(context.Background(), "{}")
	if err != nil {
		t.Fatalf("failure must degrade to text: %v", err)
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("error details leaked: %q", out)
	}
}
