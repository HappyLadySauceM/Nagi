package infrastructure

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

func TestTransportClosesOnHeartbeatTimeout(t *testing.T) {
	// 验证窗口内没有任何入站帧时看门狗断开连接并触发重连。
	var upgrades atomic.Int64
	server := startFakeSnowLuma(t, "", func(conn *websocket.Conn) {
		upgrades.Add(1)
		// never send any frame; keep the connection open until dropped
		// 从不发送任何帧；保持连接直到被看门狗断开
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	tr := NewTransport(Options{
		URL:              wsURL,
		HeartbeatTimeout: 300 * time.Millisecond,
		ReconnectInitial: 20 * time.Millisecond,
		ReconnectMax:     50 * time.Millisecond,
	}, nil)
	tr.Attach(&recordedSinks{}, &recordedSinks{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	waitFor(t, func() bool { return upgrades.Load() >= 2 })
	if upgrades.Load() < 2 {
		t.Fatalf("expected reconnect on heartbeat timeout, upgrades = %d", upgrades.Load())
	}
	cancel()
}

func TestTransportKeepsAliveOnHeartbeats(t *testing.T) {
	// 验证持续心跳下看门狗不断开连接。
	var upgrades atomic.Int64
	server := startFakeSnowLuma(t, "", func(conn *websocket.Conn) {
		upgrades.Add(1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		hb := []byte(`{"time":1,"self_id":10000,"post_type":"meta_event","meta_event_type":"heartbeat","heartbeat_interval":100}`)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.TextMessage, hb); err != nil {
					return
				}
			}
		}
	})
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	tr := NewTransport(Options{
		URL:              wsURL,
		HeartbeatTimeout: 800 * time.Millisecond,
		ReconnectInitial: 20 * time.Millisecond,
		ReconnectMax:     50 * time.Millisecond,
	}, nil)
	tr.Attach(&recordedSinks{}, &recordedSinks{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	// observe for a bounded window; any reconnect within it is a failure
	// 在有界窗口内观察；窗口内任何重连都视为失败
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if upgrades.Load() > 1 {
			t.Fatalf("connection dropped despite heartbeats, upgrades = %d", upgrades.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if upgrades.Load() != 1 {
		t.Fatalf("upgrades = %d, want exactly 1", upgrades.Load())
	}
	cancel()
}

func TestMapEventParsesCQStringMessage(t *testing.T) {
	// 验证字符串 message 按 CQ 规则解析为段，raw_message 保留原文。
	event, err := mapEvent([]byte(`{"time":1,"self_id":10000,"post_type":"message","message_type":"group","group_id":456,"user_id":10001,"message":"[CQ:at,qq=10002] hello","raw_message":"[CQ:at,qq=10002] hello"}`))
	if err != nil {
		t.Fatalf("map event: %v", err)
	}
	if len(event.Segments) != 2 {
		t.Fatalf("segments = %+v", event.Segments)
	}
	if event.Segments[0].Type != obdomain.SegAt || event.Segments[0].Data["qq"] != "10002" {
		t.Fatalf("at segment = %+v", event.Segments[0])
	}
	if event.Segments[1].Data["text"] != " hello" {
		t.Fatalf("text segment = %+v", event.Segments[1])
	}
	if event.RawMessage != "[CQ:at,qq=10002] hello" {
		t.Fatalf("raw message = %q", event.RawMessage)
	}
	if event.GroupID != 456 {
		t.Fatalf("group id = %d", event.GroupID)
	}
}

func TestMapEventParsesWireArrayMessage(t *testing.T) {
	// 验证段数组 message 经统一解码，复杂 data 值保留 JSON 文本。
	event, err := mapEvent([]byte(`{"post_type":"message","message":[{"type":"text","data":{"text":"hi"}},{"type":"json","data":{"data":{"a":[1,2]}}}]}`))
	if err != nil {
		t.Fatalf("map event: %v", err)
	}
	if len(event.Segments) != 2 {
		t.Fatalf("segments = %+v", event.Segments)
	}
	if event.Segments[0].Data["text"] != "hi" {
		t.Fatalf("text segment = %+v", event.Segments[0])
	}
	if event.Segments[1].Data["data"] != `{"a":[1,2]}` {
		t.Fatalf("json segment = %+v", event.Segments[1])
	}
}

func TestEncodeSegmentsRestoresNodeContent(t *testing.T) {
	// 验证节点 content 由 JSON 文本还原为真实数组。
	node, err := obdomain.CustomNode(10001, "A", []obdomain.Segment{obdomain.Text("one")})
	if err != nil {
		t.Fatalf("custom node: %v", err)
	}
	encoded := encodeSegments([]obdomain.Segment{node})
	data, ok := encoded[0]["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", encoded[0]["data"])
	}
	content, ok := data["content"].([]any)
	if !ok {
		t.Fatalf("content not restored: %#v", data["content"])
	}
	first, ok := content[0].(map[string]any)
	if !ok || first["type"] != "text" {
		t.Fatalf("content[0] = %#v", content[0])
	}
	textData, ok := first["data"].(map[string]any)
	if !ok || textData["text"] != "one" {
		t.Fatalf("content data = %#v", textData)
	}
}
