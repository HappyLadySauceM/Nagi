// Tests for the AI ingress handler's privacy and routing behavior.
// AI 入站处理器隐私与路由行为的测试。
package infrastructure

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/HappyLadySauceM/QQBot/internal/conversation/domain"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
	scopedomain "github.com/HappyLadySauceM/QQBot/internal/scope/domain"
)

// discardLogger drops log output in tests.
// discardLogger 在测试中丢弃日志输出。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeStatus returns a fixed connection snapshot.
// fakeStatus 返回固定的连接快照。
type fakeStatus struct{ status obcontracts.BotStatus }

func (f *fakeStatus) Status() obcontracts.BotStatus { return f.status }

// fakeAuthorizer resolves policies from a map and records calls.
// fakeAuthorizer 从映射解析策略并记录调用。
type fakeAuthorizer struct {
	policies map[string]scopedomain.Policy
	resolves int
}

func (f *fakeAuthorizer) Resolve(_ context.Context, botID int64, t scopedomain.ScopeType, id int64) (scopedomain.Policy, error) {
	f.resolves++
	return f.policies[scopedomainScopeKey(botID, string(t), id)], nil
}

// scopedomainScopeKey is a test-only policy key helper.
// scopedomainScopeKey 是仅用于测试的策略键。
func scopedomainScopeKey(botID int64, typ string, id int64) string {
	return itoa(botID) + ":" + typ + ":" + itoa(id)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// fakeIngestor records ingest inputs.
// fakeIngestor 记录入库输入。
type fakeIngestor struct {
	inputs []domain.IngestInput
}

func (f *fakeIngestor) Ingest(_ context.Context, in domain.IngestInput) (domain.IngestResult, error) {
	f.inputs = append(f.inputs, in)
	return domain.IngestResult{ConversationID: "c1", MessageID: 1, JobID: 1}, nil
}

func groupEvent(senderID obdomain.UserID, selfID obdomain.BotID) obdomain.Event {
	return obdomain.Event{
		Post:       obdomain.PostMessage,
		Type:       obdomain.TypeGroup,
		SelfID:     selfID,
		GroupID:    700,
		MessageID:  5001,
		UserID:     senderID,
		Sender:     obdomain.Sender{ID: senderID},
		RawMessage: "你好",
	}
}

func newTestIngress(t *testing.T, auth *fakeAuthorizer, ingest *fakeIngestor, status obcontracts.BotStatus) *Ingress {
	t.Helper()
	h, err := NewIngress(ingest, auth, &fakeStatus{status: status}, discardLogger())
	if err != nil {
		t.Fatalf("new ingress: %v", err)
	}
	return h
}

// TestIngressSkipsOwnMessages verifies that events from the bot itself never
// reach scope resolution or ingest.
// TestIngressSkipsOwnMessages 验证机器人自身事件不会进入作用域解析或入库。
func TestIngressSkipsOwnMessages(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest, obcontracts.BotStatus{Connected: true, SelfIDKnown: true, SelfID: 100})

	event := groupEvent(100, 100) // sender == self
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if auth.resolves != 0 || len(ingest.inputs) != 0 {
		t.Fatalf("own message must be skipped: resolves=%d ingests=%d", auth.resolves, len(ingest.inputs))
	}
}

// TestIngressDropsUnauthorizedContent verifies that disabled scopes are neither
// resolved into ingest nor persisted (privacy red line).
// TestIngressDropsUnauthorizedContent 验证未启用的作用域不进入入库（隐私红线）。
func TestIngressDropsUnauthorizedContent(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{
		scopedomainScopeKey(100, "group", 700): {Enabled: false},
	}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest, obcontracts.BotStatus{Connected: true, SelfIDKnown: true, SelfID: 100})

	if err := h.Handle(context.Background(), groupEvent(200, 100)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(ingest.inputs) != 0 {
		t.Fatalf("unauthorized content must not be ingested: %+v", ingest.inputs)
	}
}

// TestIngressIgnoresEventsBeforeSelfKnown verifies the handler is inert until the
// bot id is known.
// TestIngressIgnoresEventsBeforeSelfKnown 验证在获知机器人 ID 前处理器不动作。
func TestIngressIgnoresEventsBeforeSelfKnown(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest, obcontracts.BotStatus{})

	if err := h.Handle(context.Background(), groupEvent(200, 0)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if auth.resolves != 0 || len(ingest.inputs) != 0 {
		t.Fatal("unknown self id must short-circuit")
	}
}

// TestIngressIngestsAuthorizedGroupMessage verifies kind, target and text mapping
// for an enabled scope with storage consent.
// TestIngressIngestsAuthorizedGroupMessage 验证已授权作用域的类型、目标与文本映射。
func TestIngressIngestsAuthorizedGroupMessage(t *testing.T) {
	auth := &fakeAuthorizer{policies: map[string]scopedomain.Policy{
		scopedomainScopeKey(100, "group", 700): {Enabled: true, StoreMessages: true},
	}}
	ingest := &fakeIngestor{}
	h := newTestIngress(t, auth, ingest, obcontracts.BotStatus{Connected: true, SelfIDKnown: true, SelfID: 100})

	event := groupEvent(200, 100)
	if !h.Match(event) {
		t.Fatal("message event should match")
	}
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(ingest.inputs) != 1 {
		t.Fatalf("expected one ingest, got %d", len(ingest.inputs))
	}
	in := ingest.inputs[0]
	if in.BotID != 100 || in.Kind != domain.KindGroup || in.TargetID != 700 ||
		in.SenderID != 200 || in.PlatformMessageID != 5001 || in.Text != "你好" {
		t.Fatalf("ingest input = %+v", in)
	}
}
