package domain

import (
	"encoding/json"
	"testing"
)

func TestParseCQPlainTextWithEscapes(t *testing.T) {
	// 验证纯文本按 CQ 规则反转义为单个文本段。
	got := ParseCQ("a&amp;b&#91;c&#93;d")
	if len(got) != 1 || got[0].Type != SegText || got[0].Data["text"] != "a&b[c]d" {
		t.Fatalf("segments = %+v", got)
	}
}

func TestParseCQCodeWithParams(t *testing.T) {
	// 验证基本码与参数反转义（含逗号）。
	got := ParseCQ("[CQ:at,qq=10001]hi[CQ:image,file=abc.png,urgent&#44;level=yes]")
	if len(got) != 3 {
		t.Fatalf("segments = %+v", got)
	}
	if got[0].Type != SegAt || got[0].Data["qq"] != "10001" {
		t.Fatalf("at segment = %+v", got[0])
	}
	if got[1].Type != SegText || got[1].Data["text"] != "hi" {
		t.Fatalf("text segment = %+v", got[1])
	}
	if got[2].Data["file"] != "abc.png" || got[2].Data["urgent,level"] != "yes" {
		t.Fatalf("image segment = %+v", got[2])
	}
}

func TestParseCQMalformedDegradesToText(t *testing.T) {
	// 验证未闭合与畸形码整体降级为字面文本。
	cases := []string{
		"[CQ:at,qq=10001",
		"[CQ:]world",
		"[CQ:at,qq]",
		"[CQ:,k=v]",
	}
	for _, raw := range cases {
		got := ParseCQ("x" + raw)
		if len(got) != 1 || got[0].Type != SegText {
			t.Fatalf("raw %q: segments = %+v", raw, got)
		}
		if got[0].Data["text"] != "x"+raw {
			t.Fatalf("raw %q: text = %q", raw, got[0].Data["text"])
		}
	}
}

func TestParseCQEmptyReturnsNil(t *testing.T) {
	// 验证空字符串返回 nil。
	if got := ParseCQ(""); got != nil {
		t.Fatalf("segments = %+v", got)
	}
}

func TestParseCQKeepsUnknownTypes(t *testing.T) {
	// 验证未知类型按原样保留，供扩展段使用。
	got := ParseCQ("[CQ:mystery,foo=bar]")
	if len(got) != 1 || got[0].Type != SegmentType("mystery") || got[0].Data["foo"] != "bar" {
		t.Fatalf("segments = %+v", got)
	}
}

func TestSegmentsFromWireRoundTrip(t *testing.T) {
	// 验证线上形状与领域段双向转换。
	original := []Segment{Text("hi"), At(10001)}
	wire, err := json.Marshal(WireSegments(original))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := SegmentsFromWire(wire)
	if err != nil {
		t.Fatalf("from wire: %v", err)
	}
	if len(got) != 2 || PlainText(got) != "hi" || got[1].Data["qq"] != "10001" {
		t.Fatalf("segments = %+v", got)
	}
}

func TestSegmentsFromWireCoercesComplexValues(t *testing.T) {
	// 验证复杂 data 值保留 JSON 文本。
	got, err := SegmentsFromWire([]byte(`[{"type":"json","data":{"data":{"a":1}}}]:`))
	_ = got
	if err == nil {
		t.Fatal("expected error for trailing garbage")
	}
	got, err = SegmentsFromWire([]byte(`[{"type":"json","data":{"data":{"a":[1,2]}}}]`))
	if err != nil {
		t.Fatalf("from wire: %v", err)
	}
	if got[0].Data["data"] != `{"a":[1,2]}` {
		t.Fatalf("data = %q", got[0].Data["data"])
	}
}

func TestSegmentsFromWireNilAndEmpty(t *testing.T) {
	// 验证 null 与空输入返回 nil。
	if got, err := SegmentsFromWire(nil); got != nil || err != nil {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	if got, err := SegmentsFromWire([]byte("null")); got != nil || err != nil {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}

func TestEventAccessorsReadExtra(t *testing.T) {
	// 验证事件访问器从 Extra 结构化读取。
	event := Event{
		Post: PostNotice,
		Extra: map[string]any{
			"notice_type": "group_ban",
			"operator_id": float64(10002),
			"duration":    float64(600),
			"flag":        "flag-value",
			"comment":     "hello",
		},
	}
	if event.NoticeType() != NoticeGroupBan {
		t.Fatalf("notice type = %q", event.NoticeType())
	}
	if event.OperatorID() != 10002 {
		t.Fatalf("operator id = %d", event.OperatorID())
	}
	if event.BanDurationSeconds() != 600 {
		t.Fatalf("duration = %d", event.BanDurationSeconds())
	}
	if event.Flag() != "flag-value" || event.Comment() != "hello" {
		t.Fatalf("flag = %q comment = %q", event.Flag(), event.Comment())
	}
	request := Event{Post: PostRequest, Extra: map[string]any{"request_type": "group"}}
	if request.RequestType() != RequestGroup {
		t.Fatalf("request type = %q", request.RequestType())
	}
	if event.RequestType() != "" {
		t.Fatal("notice event must not expose request type")
	}
	if request.NoticeType() != "" {
		t.Fatal("request event must not expose notice type")
	}
}

func TestEventHeartbeatAccessors(t *testing.T) {
	// 验证心跳元事件的类型与间隔读取。
	event := Event{
		Post: PostMeta,
		Extra: map[string]any{
			"meta_event_type":    "heartbeat",
			"heartbeat_interval": float64(5000),
		},
	}
	if !event.IsHeartbeat() || event.MetaType() != MetaHeartbeat {
		t.Fatalf("meta type = %q", event.MetaType())
	}
	if event.HeartbeatIntervalMillis() != 5000 {
		t.Fatalf("interval = %d", event.HeartbeatIntervalMillis())
	}
	lifecycle := Event{Post: PostMeta, Extra: map[string]any{"meta_event_type": "lifecycle"}}
	if lifecycle.HeartbeatIntervalMillis() != 0 {
		t.Fatal("non-heartbeat event must not expose interval")
	}
}
