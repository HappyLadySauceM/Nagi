package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const frameLimit = 1 << 20

type fields map[string]json.RawMessage

// object rejects ambiguous envelope keys before any frame is inspected or relayed.
// object 在检查或转发前拒绝字段歧义。
func object(raw []byte) fields {
	if !utf8.Valid(raw) {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	out := make(fields)
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || key == "" || key != strings.ToLower(key) || out[key] != nil {
			return nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil
		}
		out[key] = value
	}
	if _, err := d.Token(); err != nil || d.Decode(new(json.RawMessage)) != io.EOF {
		return nil
	}
	return out
}

func (f fields) text(key string) string {
	var text string
	_ = json.Unmarshal(f[key], &text)
	return text
}

func (f fields) id(key string) int64 {
	var id int64
	if json.Unmarshal(f[key], &id) != nil {
		return 0
	}
	return id
}

func (f fields) intValue(key string) (int64, bool) {
	var value *int64
	err := json.Unmarshal(f[key], &value)
	if err != nil || value == nil {
		return 0, false
	}
	return *value, true
}

func validText(text string) bool {
	return text != "" && utf8.RuneCountInString(text) <= 500 && !strings.Contains(text, "[CQ:") &&
		!strings.ContainsFunc(text, func(r rune) bool { return r < ' ' || r == 127 || r == utf8.RuneError })
}

func plainText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil && validText(text) {
		return text
	}
	var segments []json.RawMessage
	if json.Unmarshal(raw, &segments) != nil || len(segments) != 1 {
		return ""
	}
	return textSegment(segments[0])
}

func textSegment(raw json.RawMessage) string {
	seg := object(raw)
	data := object(seg["data"])
	if len(seg) != 2 || seg.text("type") != "text" || len(data) != 1 || !validText(data.text("text")) {
		return ""
	}
	return data.text("text")
}

func (cfg filterConfig) event(f fields) bool {
	if f["echo"] != nil || f.id("self_id") != cfg.BotID || f.text("post_type") != "message" ||
		f.text("message_type") != cfg.Kind || f.id("user_id") != cfg.SenderID || f.id("message_id") == 0 ||
		plainText(f["message"]) != cfg.Trigger {
		return false
	}
	if raw := f["raw_message"]; raw != nil && f.text("raw_message") != cfg.Trigger {
		return false
	}
	if object(f["sender"]).id("user_id") != cfg.SenderID {
		return false
	}
	if cfg.Kind == "group" {
		return f.id("group_id") == cfg.TargetID && f.text("sub_type") == "normal"
	}
	return f["group_id"] == nil && f.text("sub_type") == "friend"
}

func (cfg filterConfig) meta(f fields) bool {
	if f["echo"] != nil || f.id("self_id") != cfg.BotID || f.text("post_type") != "meta_event" {
		return false
	}
	switch f.text("meta_event_type") {
	case "lifecycle":
		switch f.text("sub_type") {
		case "enable", "disable", "connect":
			return true
		}
	case "heartbeat":
		return true
	}
	return false
}

type outboundText struct {
	text    string
	mention bool
}

func (cfg filterConfig) send(f fields) (outboundText, bool) {
	params := object(f["params"])
	targetKey, action := "group_id", "send_group_msg"
	if cfg.Kind == "private" {
		targetKey, action = "user_id", "send_private_msg"
	}
	if params.id(targetKey) != cfg.TargetID {
		return outboundText{}, false
	}
	switch f.text("action") {
	case action:
		if len(params) != 2 {
			return outboundText{}, false
		}
	case "send_msg":
		if len(params) != 3 || params.text("message_type") != cfg.Kind {
			return outboundText{}, false
		}
	default:
		return outboundText{}, false
	}
	if text := plainText(params["message"]); text != "" {
		return outboundText{text: text}, true
	}
	var segments []json.RawMessage
	if cfg.Kind != "group" || json.Unmarshal(params["message"], &segments) != nil || len(segments) != 2 {
		return outboundText{}, false
	}
	at := object(segments[0])
	atData := object(at["data"])
	text := textSegment(segments[1])
	if len(at) != 2 || at.text("type") != "at" || len(atData) != 1 || atData.text("qq") != strconv.FormatInt(cfg.SenderID, 10) || text == "" {
		return outboundText{}, false
	}
	return outboundText{text: text, mention: true}, true
}

func validEcho(echo string) bool {
	return echo != "" && len(echo) <= 128 && strings.IndexFunc(echo, func(r rune) bool { return r < '!' || r > '~' }) < 0
}
