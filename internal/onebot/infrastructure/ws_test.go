package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	obdomain "github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// recordedSinks captures frames routed by the transport.
// recordedSinks 记录传输层路由出的帧。
type recordedSinks struct {
	mu        sync.Mutex
	responses []string
	events    []obdomain.Event
}

func (r *recordedSinks) HandleResponse(echo string, payload json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.responses = append(r.responses, echo)
}

func (r *recordedSinks) HandleEvent(event obdomain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordedSinks) snapshot() (responses []string, events []obdomain.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.responses...), append([]obdomain.Event(nil), r.events...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
}

// startFakeSnowLuma upgrades connections and requires the bearer token.
// startFakeSnowLuma 升级连接并校验 Bearer token。
func startFakeSnowLuma(t *testing.T, token string, handle func(conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handle(conn)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTransportRoutesResponsesAndEvents(t *testing.T) {
	// 验证帧解复用：带 echo 的帧交给 ResponseSink，事件帧交给 EventSink。
	sinks := &recordedSinks{}
	server := startFakeSnowLuma(t, "secret-token", func(conn *websocket.Conn) {
		eventFrame := `{"time":1,"self_id":10000,"post_type":"meta_event","meta_event_type":"heartbeat"}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(eventFrame)); err != nil {
			t.Errorf("write event: %v", err)
			return
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read action: %v", err)
			return
		}
		var frame map[string]any
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Errorf("unmarshal action: %v", err)
			return
		}
		if frame["action"] != "get_login_info" {
			t.Errorf("action = %v", frame["action"])
		}
		echo, _ := frame["echo"].(string)
		resp, _ := json.Marshal(map[string]any{
			"status": "ok", "retcode": 0, "echo": echo,
			"data": map[string]any{"user_id": 10000},
		})
		if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
			t.Errorf("write response: %v", err)
			return
		}
		// keep serving until the client disconnects
		// 持续读取直到客户端断开
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	tr := NewTransport(Options{
		URL:              wsURL,
		AccessToken:      "secret-token",
		ReconnectInitial: 50 * time.Millisecond,
		ReconnectMax:     100 * time.Millisecond,
	}, nil)
	tr.Attach(sinks, sinks)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	waitFor(t, tr.Connected)
	if !tr.Connected() {
		t.Fatal("transport never connected")
	}
	if err := tr.Send(ctx, obdomain.Action{Name: obdomain.ActionGetLoginInfo}, "echo-1"); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, func() bool {
		responses, events := sinks.snapshot()
		return len(responses) == 1 && len(events) == 1
	})
	responses, events := sinks.snapshot()
	if len(responses) != 1 || responses[0] != "echo-1" {
		t.Fatalf("responses = %v", responses)
	}
	if len(events) != 1 || events[0].SelfID != 10000 {
		t.Fatalf("events = %+v", events)
	}
	cancel()
}

func TestTransportReconnectsAfterDrop(t *testing.T) {
	// 验证连接被服务端断开后按退避自动重连。
	var upgrades atomic.Int64
	server := startFakeSnowLuma(t, "", func(conn *websocket.Conn) {
		upgrades.Add(1)
		_ = conn.Close()
	})
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	tr := NewTransport(Options{
		URL:              wsURL,
		ReconnectInitial: 10 * time.Millisecond,
		ReconnectMax:     20 * time.Millisecond,
	}, nil)
	tr.Attach(&recordedSinks{}, &recordedSinks{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	waitFor(t, func() bool { return upgrades.Load() >= 2 })
	if upgrades.Load() < 2 {
		t.Fatalf("expected at least 2 connections, got %d", upgrades.Load())
	}
	cancel()
}
