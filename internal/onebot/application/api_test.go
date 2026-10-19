package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// ClientService must satisfy the full typed action surface.
// ClientService 必须满足全部类型化动作接口。
var _ contracts.API = (*ClientService)(nil)

// respondWith makes the fake transport auto-respond with the given data payload.
// respondWith 让 fake 传输在发送动作后自动回指定 data 载荷。
func respondWith(svc *ClientService, tr *fakeTransport, data any) {
	tr.onSend = func(echo string) {
		raw, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "echo": echo, "data": data})
		svc.HandleResponse(echo, raw)
	}
}

func TestGetLoginInfoDecodesResponse(t *testing.T) {
	// 验证 get_login_info 的类型化解码。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, map[string]any{"user_id": 10000, "nickname": "Bot"})
	info, err := svc.GetLoginInfo(context.Background())
	if err != nil {
		t.Fatalf("get login info: %v", err)
	}
	if info.UserID != 10000 || info.Nickname != "Bot" {
		t.Fatalf("info = %+v", info)
	}
}

func TestSendMessageBuildsTypedAction(t *testing.T) {
	// 验证 send_msg 参数构造与目标类型分流。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, nil)
	if err := svc.SendMessage(context.Background(), domain.TypeGroup, 456, domain.Text("hi")); err != nil {
		t.Fatalf("send message: %v", err)
	}
	frames := tr.sentFrames()
	if len(frames) != 1 {
		t.Fatal("action was not sent")
	}
	if frames[0].action.Params["message_type"] != "group" || frames[0].action.Params["group_id"] != int64(456) {
		t.Fatalf("params = %+v", frames[0].action.Params)
	}
	if err := svc.SendMessage(context.Background(), "bad", 1, domain.Text("hi")); err == nil {
		t.Fatal("expected error for unknown message type")
	}
}

func TestGetMessageDecodesWireSegments(t *testing.T) {
	// 验证 get_msg 对段数组 message 的解码。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, map[string]any{
		"time": 1, "message_id": 42, "user_id": 10001,
		"message": []map[string]any{{"type": "text", "data": map[string]any{"text": "hi"}}},
	})
	result, err := svc.GetMessage(context.Background(), 42)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if result.MessageID != 42 || domain.PlainText(result.Segments) != "hi" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGetMessageDecodesCQString(t *testing.T) {
	// 验证 get_msg 对 CQ 字符串 message 的解码。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, map[string]any{
		"time": 1, "message_id": 42, "user_id": 10001,
		"message": "[CQ:at,qq=10002] hello",
	})
	result, err := svc.GetMessage(context.Background(), 42)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if len(result.Segments) != 2 || result.Segments[0].Type != domain.SegAt {
		t.Fatalf("segments = %+v", result.Segments)
	}
}

func TestGetForwardMessagesDecodesNodes(t *testing.T) {
	// 验证 get_forward_msg 的节点解码（content 为段数组）。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, map[string]any{
		"messages": []map[string]any{
			{"user_id": 10001, "nickname": "A", "time": 1,
				"content": []map[string]any{{"type": "text", "data": map[string]any{"text": "one"}}}},
			{"user_id": 10002, "nickname": "B", "time": 2,
				"content": "plain text"},
		},
	})
	nodes, err := svc.GetForwardMessages(context.Background(), 42)
	if err != nil {
		t.Fatalf("get forward messages: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if domain.PlainText(nodes[0].Segments) != "one" || domain.PlainText(nodes[1].Segments) != "plain text" {
		t.Fatalf("node segments = %+v / %+v", nodes[0].Segments, nodes[1].Segments)
	}
}

func TestSendGroupForwardReturnsReceipt(t *testing.T) {
	// 验证合并转发的回执解码与节点参数。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, map[string]any{"message_id": 9, "res_id": "res-1"})
	node, err := domain.CustomNode(10001, "A", []domain.Segment{domain.Text("one")})
	if err != nil {
		t.Fatalf("custom node: %v", err)
	}
	receipt, err := svc.SendGroupForward(context.Background(), 456, node)
	if err != nil {
		t.Fatalf("send forward: %v", err)
	}
	if receipt.MessageID != 9 || receipt.ResID != "res-1" {
		t.Fatalf("receipt = %+v", receipt)
	}
	frames := tr.sentFrames()
	stored, ok := frames[0].action.Params["messages"].([]domain.Segment)
	if !ok || len(stored) != 1 || stored[0].Type != domain.SegNode {
		t.Fatalf("messages param = %+v", frames[0].action.Params["messages"])
	}
	if _, err := svc.SendGroupForward(context.Background(), 456); err == nil {
		t.Fatal("expected error for empty nodes")
	}
}

func TestGetFriendListDecodesArray(t *testing.T) {
	// 验证数组形响应的解码。
	tr := newFakeTransport()
	svc := NewClientService(tr, time.Second, nil)
	respondWith(svc, tr, []map[string]any{
		{"user_id": 10001, "nickname": "A", "remark": "alpha"},
		{"user_id": 10002, "nickname": "B"},
	})
	friends, err := svc.GetFriendList(context.Background())
	if err != nil {
		t.Fatalf("get friend list: %v", err)
	}
	if len(friends) != 2 || friends[0].Remark != "alpha" || friends[1].Remark != "" {
		t.Fatalf("friends = %+v", friends)
	}
}

func TestHeartbeatVisibilityInStatus(t *testing.T) {
	// 验证心跳事件驱动状态快照。
	tr := newFakeTransport()
	svc := NewClientService(tr, 0, nil)
	if svc.Status().HeartbeatSeen {
		t.Fatal("heartbeat must be unseen initially")
	}
	svc.HandleEvent(domain.Event{
		Post:  domain.PostMeta,
		Extra: map[string]any{"meta_event_type": "heartbeat", "heartbeat_interval": float64(5000)},
	})
	status := svc.Status()
	if !status.HeartbeatSeen || status.HeartbeatIntervalMillis != 5000 {
		t.Fatalf("status = %+v", status)
	}
	if status.LastHeartbeatAt.IsZero() {
		t.Fatal("last heartbeat time missing")
	}
}

func TestDecodeJSONTreatsNullAsZero(t *testing.T) {
	// 验证 retcode=1（异步提交）时 null data 得到零值且不出错。
	var row struct {
		MessageID int64 `json:"message_id"`
	}
	if err := decodeJSON(json.RawMessage("null"), &row); err != nil {
		t.Fatalf("decode null: %v", err)
	}
	if row.MessageID != 0 {
		t.Fatalf("row = %+v", row)
	}
}
