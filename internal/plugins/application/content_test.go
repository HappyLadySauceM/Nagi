package application

import (
	"context"
	"encoding/json"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"strings"
	"testing"
)

func TestContentPermissionsAndRecursiveCost(t *testing.T) {
	cases := []struct {
		name        string
		segments    []ob.Segment
		permissions map[string]bool
		want        int
		denied      bool
	}{
		{name: "unicode text", segments: []ob.Segment{ob.Text("你好")}, want: 2},
		{name: "media costs at least one", segments: []ob.Segment{{Type: ob.SegImage, Data: map[string]string{"file": "a.jpg"}}}, permissions: map[string]bool{"qq.content.media": true}, want: 1},
		{name: "media denied", segments: []ob.Segment{{Type: ob.SegImage, Data: map[string]string{"file": "a.jpg"}}}, denied: true},
		{name: "at all denied", segments: []ob.Segment{{Type: ob.SegAt, Data: map[string]string{"qq": "all"}}}, denied: true},
		{name: "links denied", segments: []ob.Segment{ob.Text("https://example.com")}, denied: true},
		{name: "encoded links denied", segments: []ob.Segment{ob.Text("https%253A%252F%252Fexample.com")}, denied: true},
		{name: "nested at all denied", segments: []ob.Segment{{Type: ob.SegNode, Data: map[string]string{"content": `[{"type":"at","data":{"qq":"all"}}]`}}}, permissions: map[string]bool{"qq.content.media": true}, denied: true},
		{name: "nested cost", segments: []ob.Segment{{Type: ob.SegNode, Data: map[string]string{"content": `[{"type":"text","data":{"text":"你好"}},{"type":"image","data":{"file":"a.jpg"}}]`}}}, permissions: map[string]bool{"qq.content.media": true}, want: 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := WithScope(context.Background(), Scope{Permissions: c.permissions})
			cost, err := CheckMessageContent(ctx, ob.PublicCommand{Segments: c.segments})
			if (err != nil) != c.denied {
				t.Fatalf("cost=%d err=%v", cost, err)
			}
			if err == nil && cost != c.want {
				t.Fatalf("cost=%d want=%d", cost, c.want)
			}
		})
	}
}
func TestCQContentCannotBypassPermissions(t *testing.T) {
	for _, text := range []string{"[CQ:at,qq=all]", "[CQ:image,file=a.jpg]", "[CQ:node,content=&#91;{\"type\":\"at\"&#44;\"data\":{\"qq\":\"all\"}}&#93;]"} {
		_, err := CheckMessageContent(WithScope(context.Background(), Scope{}), ob.PublicCommand{Text: &text})
		if err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
	text := "[CQ:at,qq=all]"
	cost, err := CheckMessageContent(WithScope(context.Background(), Scope{}), ob.PublicCommand{Text: &text, AutoEscape: true})
	if err != nil || cost != len(text) {
		t.Fatalf("literal text: %d %v", cost, err)
	}
	raw, _ := json.Marshal([]map[string]any{{"type": "image", "data": map[string]string{"file": strings.Repeat("x", 65536)}}})
	segments, _ := ob.SegmentsFromWire(raw)
	if _, err := CheckMessageContent(WithScope(context.Background(), Scope{Permissions: map[string]bool{"qq.content.media": true}}), ob.PublicCommand{Segments: segments}); err == nil {
		t.Fatal("accepted oversized message")
	}
}
