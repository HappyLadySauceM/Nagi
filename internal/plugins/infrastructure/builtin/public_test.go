package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	oa "github.com/HappyLadySauceM/Nagi/internal/onebot/application"
	oc "github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	oi "github.com/HappyLadySauceM/Nagi/internal/onebot/infrastructure"
	pa "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	pc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	"github.com/gorilla/websocket"
)

// TestPublicToolsStandardFrames uses a real WebSocket with standard-shaped responses.
// TestPublicToolsStandardFrames 使用真实 WebSocket 和标准响应验证工具调用。
func TestPublicToolsStandardFrames(t *testing.T) {
	cases := []struct{ name, args, want, response string }{
		{"send_private_msg", `{"user_id":22,"message":"hello","auto_escape":false}`, `{"user_id":22,"message":"hello","auto_escape":false}`, `{"message_id":-1}`},
		{"send_group_msg", `{"message":[{"type":"text","data":{"text":"你好"}}]}`, `{"group_id":11,"message":[{"type":"text","data":{"text":"你好"}}]}`, `{"message_id":-2}`},
		{"send_msg", `{"group_id":11,"message":"[CQ:at,qq=all]hi"}`, `{"message_type":"group","group_id":11,"message":"[CQ:at,qq=all]hi","auto_escape":false}`, `{"message_id":-3}`},
		{"delete_msg", `{"message_id":-42}`, `{"message_id":-42}`, `null`},
		{"get_msg", `{"message_id":-42}`, `{"message_id":-42}`, `{"time":1,"message_type":"group","message_id":-42,"real_id":9,"sender":{"user_id":22},"message":[{"type":"text","data":{"text":"hi"}}]}`},
		{"get_forward_msg", `{"id":"forward-abc"}`, `{"id":"forward-abc"}`, `{"message":[{"type":"node","data":{"user_id":22,"nickname":"A","content":[{"type":"text","data":{"text":"hi"}}]}}]}`},
		{"send_like", `{"user_id":22}`, `{"user_id":22,"times":1}`, `null`},
		{"set_group_kick", `{"user_id":22}`, `{"group_id":11,"user_id":22,"reject_add_request":false}`, `null`},
		{"set_group_ban", `{"user_id":22,"duration":0}`, `{"group_id":11,"user_id":22,"duration":0}`, `null`},
		{"set_group_anonymous_ban", `{"anonymous":{"id":1,"name":"A","flag":"f"}}`, `{"group_id":11,"anonymous":{"id":1,"name":"A","flag":"f"},"duration":1800}`, `null`},
		{"set_group_whole_ban", `{"enable":false}`, `{"group_id":11,"enable":false}`, `null`},
		{"set_group_admin", `{"user_id":22}`, `{"group_id":11,"user_id":22,"enable":true}`, `null`},
		{"set_group_anonymous", `{}`, `{"group_id":11,"enable":true}`, `null`},
		{"set_group_card", `{"user_id":22,"card":""}`, `{"group_id":11,"user_id":22,"card":""}`, `null`},
		{"set_group_name", `{"group_name":"new"}`, `{"group_id":11,"group_name":"new"}`, `null`},
		{"set_group_leave", `{"is_dismiss":false}`, `{"group_id":11,"is_dismiss":false}`, `null`},
		{"set_group_special_title", `{"user_id":22,"special_title":""}`, `{"group_id":11,"user_id":22,"special_title":"","duration":-1}`, `null`},
		{"set_friend_add_request", `{"flag":"f","approve":false}`, `{"flag":"f","approve":false,"remark":""}`, `null`},
		{"set_group_add_request", `{"flag":"f","sub_type":"invite","approve":true}`, `{"flag":"f","sub_type":"invite","approve":true,"reason":""}`, `null`},
		{"get_login_info", `{}`, `{}`, `{"user_id":100,"nickname":"bot"}`},
		{"get_stranger_info", `{"user_id":22}`, `{"user_id":22,"no_cache":false}`, `{"user_id":22,"nickname":"A","sex":"unknown","age":0}`},
		{"get_friend_list", `{}`, `{}`, `[{"user_id":22,"nickname":"A","remark":"B"}]`},
		{"get_group_info", `{"no_cache":true}`, `{"group_id":11,"no_cache":true}`, `{"group_id":11,"group_name":"G","member_count":2,"max_member_count":200}`},
		{"get_group_list", `{}`, `{}`, `[{"group_id":11,"group_name":"G","member_count":2,"max_member_count":200}]`},
		{"get_group_member_info", `{"user_id":22}`, `{"group_id":11,"user_id":22,"no_cache":false}`, `{"group_id":11,"user_id":22,"nickname":"A","card":"B","sex":"unknown","age":0,"area":"","join_time":1,"last_sent_time":2,"level":"1","role":"admin","unfriendly":false,"title":"T","title_expire_time":-1,"card_changeable":true}`},
		{"get_group_member_list", `{}`, `{"group_id":11}`, `[{"group_id":11,"user_id":22}]`},
		{"get_group_honor_info", `{"type":"all"}`, `{"group_id":11,"type":"all"}`, `{"group_id":11,"talkative_list":[]}`},
		{"get_cookies", `{"domain":"qq.com"}`, `{"domain":"qq.com"}`, `{"cookies":"test-cookie-secret"}`},
		{"get_csrf_token", `{}`, `{}`, `{"token":123}`},
		{"get_credentials", `{}`, `{"domain":""}`, `{"cookies":"test-cookie-secret","csrf_token":123}`},
		{"get_record", `{"file":"a.silk","out_format":"mp3"}`, `{"file":"a.silk","out_format":"mp3"}`, `{"file":"/record/a.mp3"}`},
		{"get_image", `{"file":"a.jpg"}`, `{"file":"a.jpg"}`, `{"file":"/image/a.jpg"}`},
		{"can_send_image", `{}`, `{}`, `{"yes":true}`},
		{"can_send_record", `{}`, `{}`, `{"yes":false}`},
		{"get_status", `{}`, `{}`, `{"online":null,"good":false}`},
		{"get_version_info", `{}`, `{}`, `{"app_name":"test","app_version":"1","protocol_version":"v11"}`},
		{"set_restart", `{"delay":0}`, `{"delay":0}`, `null`},
		{"clean_cache", `{}`, `{}`, `null`},
	}
	responses := map[string]json.RawMessage{}
	for _, c := range cases {
		responses[c.name] = json.RawMessage(c.response)
	}
	frames := make(chan map[string]json.RawMessage, 50)
	upgrade := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var frame map[string]json.RawMessage
			if conn.ReadJSON(&frame) != nil {
				return
			}
			frames <- frame
			var name string
			_ = json.Unmarshal(frame["action"], &name)
			status := "ok"
			retcode := 0
			if name == "set_restart" {
				status = "async"
				retcode = 1
			}
			if conn.WriteJSON(map[string]any{"status": status, "retcode": retcode, "echo": frame["echo"], "data": responses[name]}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	tr := oi.NewTransport(oi.Options{URL: "ws" + strings.TrimPrefix(server.URL, "http")}, nil)
	api := oa.NewClientService(tr, time.Second, nil)
	tr.Attach(api, api)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer api.Close()
	done := make(chan struct{})
	go func() { defer close(done); tr.Run(ctx) }()
	defer func() { cancel(); _ = api.Close(); <-done }()
	deadline := time.Now().Add(time.Second)
	for !tr.Connected() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	provider := NewPublicProvider(PublicOptions{Account: func(botID int64) (oc.PublicAPI, bool) { return api, botID == 100 }, Send: api.ExecutePublic})
	tools, err := provider.Tools(ctx)
	if err != nil || len(tools) != 38 {
		t.Fatalf("catalog: %d %v", len(tools), err)
	}
	byID := map[string]pc.RegisteredTool{}
	for _, tool := range tools {
		byID[tool.Descriptor.ID] = tool
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, ok := byID["qq."+c.name]
			if !ok {
				t.Fatal("missing tool")
			}
			grants := map[string]bool{tool.Descriptor.Permission: true, "qq.cross_conversation": true, "qq.content.links": true, "qq.content.at_all": true, "qq.content.media": true}
			scope := pa.Scope{BotID: 100, Kind: "group", TargetID: 11, Permissions: grants}
			result, err := pa.Guard(scope, tool.Descriptor, tool.Invoke)(ctx, c.args)
			if err != nil {
				t.Fatal(err)
			}
			var frame map[string]json.RawMessage
			select {
			case frame = <-frames:
			case <-time.After(time.Second):
				t.Fatalf("no frame: %s", result)
			}
			var got, want any
			_ = json.Unmarshal(frame["params"], &got)
			_ = json.Unmarshal([]byte(c.want), &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("params %s want %s", frame["params"], c.want)
			}
			var response struct {
				Status string `json:"status"`
				Data   any    `json:"data"`
			}
			if json.Unmarshal([]byte(result), &response) != nil {
				t.Fatal(result)
			}
			var data any
			_ = json.Unmarshal([]byte(c.response), &data)
			if !reflect.DeepEqual(response.Data, data) {
				t.Fatalf("response changed: %s", result)
			}
			if c.name == "set_restart" && response.Status != "async" {
				t.Fatal("async reported complete")
			}
		})
	}
}

type recordingPublicAPI struct {
	calls   int
	command ob.PublicCommand
}

func (a *recordingPublicAPI) ExecutePublic(_ context.Context, c ob.PublicCommand) (ob.PublicResult, error) {
	a.calls++
	a.command = c
	return ob.PublicResult{Data: map[string]any{"group_id": int64(c.GroupID)}}, nil
}

func TestPublicPermissionsAndTargets(t *testing.T) {
	api := &recordingPublicAPI{}
	tools, _ := NewPublicProvider(PublicOptions{Account: func(botID int64) (oc.PublicAPI, bool) { return api, botID == 100 }}).Tools(context.Background())
	byID := map[string]pc.RegisteredTool{}
	for _, tool := range tools {
		byID[tool.Descriptor.ID] = tool
	}
	defaults := pa.ResolvePermissions(tools, nil, nil)
	if defaults["qq.api.get_cookies"] || defaults["qq.api.get_login_info"] || defaults["qq.cross_conversation"] || defaults["qq.content.links"] || !defaults["qq.group.read"] {
		t.Fatalf("bad defaults: %v", defaults)
	}
	cases := []struct {
		name, tool, args, kind string
		bot, target            int64
		grants                 map[string]bool
		revoke                 bool
		wantCalls              int
		wantTarget             ob.GroupID
	}{
		{name: "ungranted", tool: "get_group_info", args: `{}`, kind: "group", bot: 100, target: 11, wantCalls: 0},
		{name: "current", tool: "get_group_info", args: `{}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.read": true}, wantCalls: 1, wantTarget: 11},
		{name: "cross denied", tool: "get_group_info", args: `{"group_id":12}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.read": true}},
		{name: "only source grants needed", tool: "get_group_info", args: `{"group_id":12}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.read": true, "qq.cross_conversation": true}, wantCalls: 1, wantTarget: 12},
		{name: "private may explicitly target group", tool: "get_group_info", args: `{"group_id":12}`, kind: "private", bot: 100, target: 22, grants: map[string]bool{"qq.group.read": true, "qq.cross_conversation": true}, wantCalls: 1, wantTarget: 12},
		{name: "private has no default group", tool: "get_group_info", args: `{}`, kind: "private", bot: 100, target: 22, grants: map[string]bool{"qq.group.read": true, "qq.cross_conversation": true}},
		{name: "other account unavailable", tool: "get_group_info", args: `{}`, kind: "group", bot: 101, target: 11, grants: map[string]bool{"qq.group.read": true}},
		{name: "live permission revoked", tool: "get_group_info", args: `{}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.read": true}, revoke: true},
		{name: "message ownership unknown", tool: "get_msg", args: `{"message_id":-1}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.api.get_msg": true}},
		{name: "model cannot set bot", tool: "get_group_info", args: `{"bot_id":101}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.read": true}},
		{name: "explicit false retained", tool: "set_group_whole_ban", args: `{"enable":false}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.admin.wholeban": true}, wantCalls: 1, wantTarget: 11},
		{name: "cross permission revoked", tool: "get_group_info", args: `{"group_id":12}`, kind: "group", bot: 100, target: 11, grants: map[string]bool{"qq.group.read": true, "qq.cross_conversation": true}, revoke: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api.calls = 0
			tool := byID["qq."+c.tool]
			scope := pa.Scope{BotID: c.bot, Kind: c.kind, TargetID: c.target, Permissions: c.grants, Reauthorize: func(_ context.Context, d pc.ToolDescriptor) error {
				if c.revoke {
					return fmt.Errorf("revoked")
				}
				return nil
			}}
			result, err := pa.Guard(scope, tool.Descriptor, tool.Invoke)(context.Background(), c.args)
			if err != nil {
				t.Fatal(err)
			}
			if api.calls != c.wantCalls {
				t.Fatalf("calls=%d want=%d output=%s", api.calls, c.wantCalls, result)
			}
			if api.calls > 0 && api.command.GroupID != c.wantTarget {
				t.Fatalf("wrong target: %v", api.command)
			}
			if c.name == "explicit false retained" && api.command.Enable {
				t.Fatal("false replaced by default")
			}
		})
	}
}

// TestPublicLeaveNeverDissolvesGroup keeps the irreversible branch out of the
// tool surface, matching the narrowed leave tool.
// TestPublicLeaveNeverDissolvesGroup 保持不可逆分支不进入工具面，与收窄的退群工具一致。
func TestPublicLeaveNeverDissolvesGroup(t *testing.T) {
	api := &recordingPublicAPI{}
	tools, _ := NewPublicProvider(PublicOptions{Account: func(botID int64) (oc.PublicAPI, bool) { return api, botID == 100 }}).Tools(context.Background())
	byID := map[string]pc.RegisteredTool{}
	for _, tool := range tools {
		byID[tool.Descriptor.ID] = tool
	}
	tool := byID["qq.set_group_leave"]
	scope := pa.Scope{BotID: 100, Kind: "group", TargetID: 11, Permissions: map[string]bool{tool.Descriptor.Permission: true}}
	result, err := pa.Guard(scope, tool.Descriptor, tool.Invoke)(context.Background(), `{"is_dismiss":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if api.calls != 0 || !strings.Contains(result, "不支持解散群") {
		t.Fatalf("dismiss not refused: result=%s calls=%d", result, api.calls)
	}
}

// TestPublicRequestApprovalNeedsCrossConversation guards decisions that reach
// beyond the source conversation.
// TestPublicRequestApprovalNeedsCrossConversation 保护超出来源会话的审批动作。
func TestPublicRequestApprovalNeedsCrossConversation(t *testing.T) {
	api := &recordingPublicAPI{}
	tools, _ := NewPublicProvider(PublicOptions{Account: func(botID int64) (oc.PublicAPI, bool) { return api, botID == 100 }}).Tools(context.Background())
	byID := map[string]pc.RegisteredTool{}
	for _, tool := range tools {
		byID[tool.Descriptor.ID] = tool
	}
	tool := byID["qq.set_group_add_request"]
	scope := pa.Scope{BotID: 100, Kind: "group", TargetID: 11, Permissions: map[string]bool{tool.Descriptor.Permission: true}}
	result, err := pa.Guard(scope, tool.Descriptor, tool.Invoke)(context.Background(), `{"flag":"f","sub_type":"invite","approve":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if api.calls != 0 || !strings.Contains(result, "跨会话") {
		t.Fatalf("approval without cross permission: result=%s calls=%d", result, api.calls)
	}
}

// TestPublicDestructiveDefaultsMustBeExplicit refuses the OneBot defaults that
// would approve or enable something without the model asking for it.
// TestPublicDestructiveDefaultsMustBeExplicit 拒绝会让模型"默认同意/默认开启"的
// OneBot 缺省值。
func TestPublicDestructiveDefaultsMustBeExplicit(t *testing.T) {
	api := &recordingPublicAPI{}
	tools, _ := NewPublicProvider(PublicOptions{Account: func(botID int64) (oc.PublicAPI, bool) { return api, botID == 100 }}).Tools(context.Background())
	byID := map[string]pc.RegisteredTool{}
	for _, tool := range tools {
		byID[tool.Descriptor.ID] = tool
	}
	cases := map[string]string{
		"qq.set_group_whole_ban":    `{}`,
		"qq.set_group_add_request":  `{"flag":"f","sub_type":"invite"}`,
		"qq.set_friend_add_request": `{"flag":"f"}`,
	}
	for id, args := range cases {
		tool := byID[id]
		scope := pa.Scope{BotID: 100, Kind: "group", TargetID: 11, Permissions: map[string]bool{tool.Descriptor.Permission: true, "qq.cross_conversation": true}}
		result, err := pa.Guard(scope, tool.Descriptor, tool.Invoke)(context.Background(), args)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !strings.Contains(result, "缺少参数") {
			t.Fatalf("%s: missing explicit parameter accepted: %s", id, result)
		}
	}
	if api.calls != 0 {
		t.Fatalf("platform calls = %d", api.calls)
	}
}
