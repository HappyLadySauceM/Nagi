package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStartupPermit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("synthetic"))
	if err := claim(dir, digest); err == nil {
		t.Fatal("missing permit accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "armed"), []byte(hex.EncodeToString(digest[:])+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := claim(dir, sha256.Sum256([]byte("different config"))); err == nil {
		t.Fatal("wrong digest accepted")
	}
	var successful atomic.Int64
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if claim(dir, digest) == nil {
				successful.Add(1)
			}
		})
	}
	wg.Wait()
	if successful.Load() != 1 || claim(dir, digest) == nil {
		t.Fatal("startup permit was reusable")
	}
}

func TestPrivatePolicy(t *testing.T) {
	cfg := filterConfig{BotID: 1, Kind: "private", TargetID: 2, SenderID: 2, Trigger: "synthetic trigger"}
	f := object([]byte(`{"post_type":"message","message_type":"private","sub_type":"friend","self_id":1,"user_id":2,"message_id":123,"message":"synthetic trigger","sender":{"user_id":2}}`))
	if !cfg.event(f) {
		t.Fatal("expected private event was rejected")
	}
	f["group_id"] = json.RawMessage(`99`)
	if cfg.event(f) {
		t.Fatal("private event with group was accepted")
	}
	delete(f, "group_id")
	delete(f, "sender")
	if cfg.event(f) {
		t.Fatal("private event without sender was accepted")
	}
	for _, raw := range []string{
		`{"action":"send_private_msg","params":{"user_id":2,"message":"[CQ:at,qq=all]"}}`,
		`{"action":"send_private_msg","params":{"user_id":2,"user_id":3,"message":"fixture"}}`,
		`{"action":"send_private_msg","params":{"User_id":2,"message":"fixture"}}`,
		`{"action":"send_msg","params":{"message_type":"group","group_id":2,"message":"fixture"}}`,
	} {
		if _, allowed := cfg.send(object([]byte(raw))); allowed {
			t.Fatal("ambiguous, wrong-target or non-text action accepted")
		}
	}
}

func TestFilterWireSession(t *testing.T) {
	for _, outcome := range []string{"ok", "failed", "disconnect", "wrong account"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := filterConfig{BotID: 1, Kind: "group", TargetID: 20, SenderID: 2, Trigger: "synthetic trigger",
				UpstreamToken: "synthetic-upstream", DownstreamToken: "synthetic-downstream"}
			var connections, sends atomic.Int64
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+cfg.UpstreamToken {
					w.WriteHeader(401)
					return
				}
				u := websocket.Upgrader{}
				c, err := u.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				connections.Add(1)
				first := map[string]any{"post_type": "message", "message_type": "group", "sub_type": "normal", "message_id": 123,
					"self_id": 1, "user_id": 2, "group_id": 19, "message": cfg.Trigger}
				_ = c.WriteJSON(first)
				first["group_id"], first["user_id"] = 20, 3
				_ = c.WriteJSON(first)
				first["user_id"], first["message"] = 2, "wrong trigger"
				_ = c.WriteJSON(first)
				_ = c.WriteJSON(map[string]any{"post_type": "meta_event", "meta_event_type": "lifecycle", "sub_type": "connect", "self_id": 1})
				first["message"], first["message_id"] = cfg.Trigger, 333
				first["time"] = "invalid"
				_ = c.WriteJSON(first)
				first["time"], first["message_id"] = 1, 444
				conflicting := []byte(`{"post_type":"message","message_type":"group","sub_type":"normal","message_id":445,"self_id":1,"user_id":2,"group_id":20,"message":"synthetic trigger","me\u017fsage":"wrong trigger","sender":{"user_id":2}}`)
				first["message_id"] = 446
				_ = c.WriteJSON(first)
				first["sender"] = map[string]int{"user_id": 2}
				first["message_id"] = 444
				if outcome != "failed" {
					_ = c.WriteMessage(websocket.TextMessage, conflicting)
					_ = c.WriteJSON(first)
				}
				var login map[string]any
				if c.ReadJSON(&login) != nil || login["action"] != "get_login_info" {
					t.Error("missing upstream account verification")
					return
				}
				bot := 1
				if outcome == "wrong account" {
					bot = 9
				}
				_ = c.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": login["echo"], "data": map[string]int{"user_id": bot}})
				if outcome == "failed" {
					_ = c.WriteMessage(websocket.TextMessage, conflicting)
					_ = c.WriteJSON(first)
				}
				_ = c.WriteJSON(map[string]any{"post_type": "meta_event", "meta_event_type": "heartbeat", "self_id": 1})
				for {
					var action map[string]any
					if c.ReadJSON(&action) != nil {
						return
					}
					sends.Add(1)
					params, _ := action["params"].(map[string]any)
					if action["action"] != "send_group_msg" || params["group_id"] != float64(20) || len(params) != 2 {
						t.Error("wrong action reached upstream")
					}
					if outcome == "disconnect" {
						return
					}
					good := map[string]any{"status": "ok", "retcode": 0, "echo": "wrong-echo", "data": map[string]int{"message_id": 900}}
					_ = c.WriteJSON(good)
					good["echo"] = action["echo"]
					if outcome == "failed" {
						good["status"], good["retcode"], good["data"] = "failed", 100, nil
					}
					_ = c.WriteJSON(good)
					_ = c.WriteJSON(good)
				}
			}))
			defer up.Close()
			cfg.Upstream = "ws" + strings.TrimPrefix(up.URL, "http")
			p := &filter{cfg: cfg, ctx: ctx}
			down := httptest.NewServer(p)
			defer func() { cancel(); p.wait(); down.Close() }()
			dial := func(token string) (*websocket.Conn, *http.Response, error) {
				dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
				return dialer.Dial("ws"+strings.TrimPrefix(down.URL, "http"), http.Header{"Authorization": {"Bearer " + token}})
			}
			_, denied, err := dial("wrong")
			if err == nil || denied == nil || denied.StatusCode != 401 {
				t.Fatal("wrong token accepted")
			}
			_ = denied.Body.Close()
			connect := func() *websocket.Conn {
				t.Helper()
				c, resp, err := dial(cfg.DownstreamToken)
				if err != nil {
					if resp != nil {
						_ = resp.Body.Close()
					}
					t.Fatal("synthetic downstream handshake failed")
				}
				_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
				return c
			}
			read := func(c *websocket.Conn) fields {
				t.Helper()
				_, raw, err := c.ReadMessage()
				if err != nil {
					t.Fatal("expected downstream frame")
				}
				return object(raw)
			}
			action := func(c *websocket.Conn, to int, echo string) {
				t.Helper()
				if !write(c, map[string]any{"action": "send_msg", "echo": echo, "params": map[string]any{"message_type": "group", "group_id": to, "message": "synthetic response"}}) {
					t.Fatal("failed to write synthetic action")
				}
			}
			c := connect()
			defer c.Close()
			if outcome == "wrong account" {
				if _, _, err := c.ReadMessage(); err == nil || sends.Load() != 0 {
					t.Fatal("wrong account was exposed")
				}
				return
			}
			if read(c).text("meta_event_type") != "lifecycle" || read(c).id("message_id") != 444 || read(c).text("meta_event_type") != "heartbeat" {
				t.Fatal("startup frame filtering or meta routing failed")
			}
			_, busy, err := dial(cfg.DownstreamToken)
			if err == nil || busy == nil || busy.StatusCode != 503 || connections.Load() != 1 {
				t.Fatal("second consumer accepted")
			}
			_ = busy.Body.Close()
			action(c, 19, "wrong-target")
			if got := read(c); got.text("echo") != "wrong-target" || got.id("retcode") != 100 || sends.Load() != 0 {
				t.Fatal("wrong-target action was not blocked")
			}
			for _, mention := range []string{"all", "3"} {
				if !write(c, map[string]any{"action": "send_group_msg", "echo": "denied-mention", "params": map[string]any{"group_id": 20,
					"message": []any{map[string]any{"type": "at", "data": map[string]string{"qq": mention}}, map[string]any{"type": "text", "data": map[string]string{"text": "fixture"}}}}}) {
					t.Fatal("failed to write synthetic action")
				}
				if got := read(c); got.text("echo") != "denied-mention" || got.id("retcode") != 100 || sends.Load() != 0 {
					t.Fatal("unauthorized mention reached upstream")
				}
			}
			if !write(c, map[string]any{"action": "send_group_msg", "echo": "send-0", "params": map[string]any{"group_id": 20,
				"message": []any{map[string]any{"type": "at", "data": map[string]string{"qq": "2"}}, map[string]any{"type": "text", "data": map[string]string{"text": "fixture"}}}}}) {
				t.Fatal("failed to write bridge-shaped action")
			}
			if outcome != "disconnect" {
				for i := 1; i <= 6; i++ {
					action(c, 20, fmt.Sprintf("send-%d", i))
				}
				seen := make(map[string]int64)
				for range 7 {
					got := read(c)
					seen[got.text("echo")] = got.id("retcode")
				}
				want := int64(0)
				if outcome == "failed" {
					want = 100
				}
				if len(seen) != 7 || seen["send-0"] != want || seen["send-6"] != 100 {
					t.Fatal("receipt or quota response mismatched")
				}
			} else if _, _, err := c.ReadMessage(); err == nil {
				t.Fatal("disconnect fabricated a receipt")
			}
			_ = c.Close()
			p.wait()
			other := connect()
			defer other.Close()
			if read(other).text("meta_event_type") != "lifecycle" || read(other).text("meta_event_type") != "heartbeat" {
				t.Fatal("reconnect replayed trigger or dropped meta event")
			}
			action(other, 20, "after-reconnect")
			if got := read(other); got.text("echo") != "after-reconnect" || got.id("retcode") != 100 || sends.Load() != 1 || connections.Load() != 2 {
				t.Fatal("reconnect restored send allowance")
			}
		})
	}
}
