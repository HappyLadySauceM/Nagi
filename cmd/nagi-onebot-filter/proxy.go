package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	obinfra "github.com/HappyLadySauceM/Nagi/internal/onebot/infrastructure"
)

const (
	frameTimeout = 5 * time.Second
	idleTimeout  = 90 * time.Second
)

type filter struct {
	cfg filterConfig
	ctx context.Context

	mu        sync.Mutex
	active    chan struct{}
	triggered bool
	spent     bool
}

func (p *filter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.URL.RawQuery != "" || r.Method != http.MethodGet || !websocket.IsWebSocketUpgrade(r) {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+p.cfg.DownstreamToken)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	if p.ctx.Err() != nil || p.active != nil {
		p.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	done := make(chan struct{})
	p.active = done
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		close(done)
		p.active = nil
	}()

	upgrader := websocket.Upgrader{HandshakeTimeout: frameTimeout}
	downstream, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer downstream.Close()
	stopDown := context.AfterFunc(p.ctx, func() { _ = downstream.Close() })
	defer stopDown()
	dialer := websocket.Dialer{HandshakeTimeout: frameTimeout}
	header := http.Header{"Authorization": {"Bearer " + p.cfg.UpstreamToken}}
	upstream, response, err := dialer.DialContext(p.ctx, p.cfg.Upstream, header)
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		log.Print("onebot filter upstream unavailable")
		return
	}
	defer upstream.Close()
	stopUp := context.AfterFunc(p.ctx, func() { _ = upstream.Close() })
	defer stopUp()
	s := &session{filter: p, up: upstream, down: downstream, upstreamEcho: newEcho()}
	upstream.SetReadLimit(frameLimit)
	downstream.SetReadLimit(frameLimit)
	if !s.verifyAccount() {
		log.Print("onebot filter account verification failed")
		return
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		s.readActions()
		_ = upstream.Close()
	}()
	s.readEvents()
	_ = downstream.Close()
	<-readDone
}

func (p *filter) wait() {
	p.mu.Lock()
	done := p.active
	p.mu.Unlock()
	if done != nil {
		<-done
	}
}

type session struct {
	*filter
	up, down  *websocket.Conn
	downWrite sync.Mutex

	// filter.mu protects the session permit and the sole pending send echo.
	// filter.mu 保护本连接的发送许可和唯一一笔待回执的 echo。
	allowed      bool
	pending      string
	usedEcho     string
	upstreamEcho string
}

func newEcho() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func (s *session) verifyAccount() bool {
	loginEcho := newEcho()
	if !write(s.up, map[string]any{"action": "get_login_info", "params": map[string]any{}, "echo": loginEcho}) {
		return false
	}
	_ = s.up.SetReadDeadline(time.Now().Add(frameTimeout))
	var buffered [][]byte
	var trigger []byte
	for {
		kind, raw, err := s.up.ReadMessage()
		if err != nil {
			return false
		}
		if kind != websocket.TextMessage {
			continue
		}
		f := object(raw)
		if f.text("echo") == loginEcho {
			retcode, ok := f.intValue("retcode")
			if !ok || retcode != 0 || f.text("status") != "ok" || f["post_type"] != nil || object(f["data"]).id("user_id") != s.cfg.BotID {
				return false
			}
			for _, meta := range buffered {
				if !s.relay(meta) {
					return false
				}
			}
			return trigger == nil || s.forwardTrigger(trigger)
		}
		if trigger == nil && s.matchesTrigger(f, raw) {
			trigger = raw
		}
		if len(buffered) < 8 && len(raw) <= 4096 && s.cfg.meta(f) {
			buffered = append(buffered, raw)
		}
	}
}

func (s *session) readEvents() {
	for {
		_ = s.up.SetReadDeadline(time.Now().Add(idleTimeout))
		kind, raw, err := s.up.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			continue
		}
		f := object(raw)
		switch {
		case s.cfg.meta(f):
			if !s.relay(raw) {
				return
			}
		case s.matchesTrigger(f, raw):
			if !s.forwardTrigger(raw) {
				return
			}
		case f.text("echo") == s.upstreamEcho && f["post_type"] == nil:
			if !s.forwardReceipt(f) {
				return
			}
		}
	}
}

func (s *session) matchesTrigger(f fields, raw []byte) bool {
	if !s.cfg.event(f) {
		return false
	}
	event, err := obinfra.DecodeEventFrame(raw)
	if err != nil || !event.IsMessage() || int64(event.SelfID) != s.cfg.BotID || string(event.Type) != s.cfg.Kind ||
		int64(event.UserID) != s.cfg.SenderID || int64(event.MessageID) != f.id("message_id") ||
		int64(event.Sender.ID) != s.cfg.SenderID || event.SubType != f.text("sub_type") ||
		event.Text() != s.cfg.Trigger || (event.RawMessage != "" && event.RawMessage != s.cfg.Trigger) {
		return false
	}
	return int64(event.GroupID) == f.id("group_id")
}

func (s *session) forwardTrigger(raw []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.triggered {
		return true
	}
	s.triggered = true
	if !s.relay(raw) {
		return false
	}
	s.allowed = true
	log.Print("onebot filter accepted trigger")
	return true
}

func (s *session) forwardReceipt(f fields) bool {
	code, ok := f.intValue("retcode")
	status := f.text("status")
	if !ok || (status != "ok" && status != "async" && status != "failed") ||
		(code == 0 && status != "ok") || (code == 1 && status != "async" && status != "ok") || (code != 0 && code != 1 && status != "failed") {
		return true
	}
	var data any
	if code == 0 {
		id := object(f["data"]).id("message_id")
		if id == 0 {
			return true
		}
		data = map[string]int64{"message_id": id}
	}
	s.mu.Lock()
	echo := s.pending
	s.pending = ""
	s.mu.Unlock()
	if echo == "" {
		return true
	}
	log.Print("onebot filter routed receipt")
	return s.reply(echo, status, code, data)
}

func (s *session) readActions() {
	for {
		kind, raw, err := s.down.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			continue
		}
		f := object(raw)
		echo := f.text("echo")
		if len(f) != 3 || object(f["params"]) == nil || !validEcho(echo) {
			continue
		}
		s.mu.Lock()
		duplicate := s.usedEcho == echo
		s.mu.Unlock()
		if duplicate {
			continue
		}
		if f.text("action") == "get_login_info" && len(object(f["params"])) == 0 {
			if !s.reply(echo, "ok", 0, map[string]int64{"user_id": s.cfg.BotID}) {
				return
			}
			continue
		}
		text, matches := s.cfg.send(f)
		if !matches || !s.reserve(echo) {
			if !s.reply(echo, "failed", 100, nil) {
				return
			}
			continue
		}
		targetKey, action := "group_id", "send_group_msg"
		if s.cfg.Kind == "private" {
			targetKey, action = "user_id", "send_private_msg"
		}
		segments := make([]map[string]any, 0, 2)
		if text.mention {
			segments = append(segments, map[string]any{"type": "at", "data": map[string]string{"qq": strconv.FormatInt(s.cfg.SenderID, 10)}})
		}
		segments = append(segments, map[string]any{"type": "text", "data": map[string]string{"text": text.text}})
		params := map[string]any{targetKey: s.cfg.TargetID, "message": segments}
		log.Print("onebot filter reserved send")
		if !write(s.up, map[string]any{"action": action, "params": params, "echo": s.upstreamEcho}) {
			return
		}
	}
}

func (s *session) reserve(echo string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed || s.spent || s.ctx.Err() != nil {
		return false
	}
	s.spent = true
	s.pending = echo
	s.usedEcho = echo
	return true
}

func (s *session) relay(raw []byte) bool {
	s.downWrite.Lock()
	defer s.downWrite.Unlock()
	_ = s.down.SetWriteDeadline(time.Now().Add(frameTimeout))
	return s.down.WriteMessage(websocket.TextMessage, raw) == nil
}

func (s *session) reply(echo, status string, code int64, data any) bool {
	raw, err := json.Marshal(map[string]any{"echo": echo, "status": status, "retcode": code, "data": data})
	return err == nil && s.relay(raw)
}

func write(conn *websocket.Conn, value any) bool {
	_ = conn.SetWriteDeadline(time.Now().Add(frameTimeout))
	return conn.WriteJSON(value) == nil
}
