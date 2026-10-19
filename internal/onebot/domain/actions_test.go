package domain

import "testing"

func TestNewSendMessageValidatesAndBuilds(t *testing.T) {
	// 验证 send_msg 两种目标类型的参数构造与输入校验。
	action, err := NewSendMessage(TypeGroup, 456, []Segment{Text("hi")})
	if err != nil {
		t.Fatalf("new send message: %v", err)
	}
	if action.Name != ActionSendMessage || action.Params["message_type"] != "group" || action.Params["group_id"] != int64(456) {
		t.Fatalf("params = %+v", action.Params)
	}
	if _, err := NewSendMessage("channel", 1, []Segment{Text("hi")}); err == nil {
		t.Fatal("expected error for unknown message type")
	}
	if _, err := NewSendMessage(TypePrivate, 0, []Segment{Text("hi")}); err == nil {
		t.Fatal("expected error for invalid target")
	}
	if _, err := NewSendMessage(TypePrivate, 10001, nil); err == nil {
		t.Fatal("expected error for empty segments")
	}
}

func TestNewSendLikeClampsTimes(t *testing.T) {
	// 验证点赞次数收敛到 1..10。
	low, err := NewSendLike(10001, 0)
	if err != nil {
		t.Fatalf("new send like: %v", err)
	}
	if low.Params["times"] != 1 {
		t.Fatalf("times = %v", low.Params["times"])
	}
	high, _ := NewSendLike(10001, 15)
	if high.Params["times"] != 10 {
		t.Fatalf("times = %v", high.Params["times"])
	}
	if _, err := NewSendLike(0, 1); err == nil {
		t.Fatal("expected error for invalid user id")
	}
}

func TestGroupAdminConstructorsValidate(t *testing.T) {
	// 验证群管动作的参数构造与校验。
	kick, err := NewSetGroupKick(456, 789, true)
	if err != nil {
		t.Fatalf("new kick: %v", err)
	}
	if kick.Params["reject_add_request"] != true {
		t.Fatalf("kick params = %+v", kick.Params)
	}
	if _, err := NewSetGroupBan(456, 789, -1); err == nil {
		t.Fatal("expected error for negative ban duration")
	}
	if _, err := NewSetGroupAnonymousBan(456, "  ", 60); err == nil {
		t.Fatal("expected error for empty anonymous flag")
	}
	if _, err := NewSetGroupName(456, " "); err == nil {
		t.Fatal("expected error for empty group name")
	}
	card, _ := NewSetGroupCard(456, 789, "")
	if card.Params["card"] != "" {
		t.Fatal("empty card must be allowed to clear the name")
	}
}

func TestNewSetGroupAddRequestValidatesSubType(t *testing.T) {
	// 验证加群请求审批的子类型校验。
	action, err := NewSetGroupAddRequest("flag", GroupRequestInvite, true, "")
	if err != nil {
		t.Fatalf("new group add request: %v", err)
	}
	if action.Params["sub_type"] != "invite" {
		t.Fatalf("sub_type = %v", action.Params["sub_type"])
	}
	if _, err := NewSetGroupAddRequest("flag", "borrow", true, ""); err == nil {
		t.Fatal("expected error for unknown sub type")
	}
	if _, err := NewSetFriendAddRequest("", true, ""); err == nil {
		t.Fatal("expected error for empty flag")
	}
}

func TestCustomNodeEncodesWireContent(t *testing.T) {
	// 验证自定义节点内嵌 content 为线上形状 JSON，可被反向解析。
	node, err := CustomNode(10001, "Alice", []Segment{Text("hi"), At(10002)})
	if err != nil {
		t.Fatalf("custom node: %v", err)
	}
	if node.Type != SegNode {
		t.Fatalf("node type = %q", node.Type)
	}
	nodes, err := SegmentsFromWire([]byte(node.Data["content"]))
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if len(nodes) != 2 || PlainText(nodes) != "hi" || nodes[1].Data["qq"] != "10002" {
		t.Fatalf("decoded nodes = %+v", nodes)
	}
	if _, err := CustomNode(0, "Alice", []Segment{Text("hi")}); err == nil {
		t.Fatal("expected error for invalid user id")
	}
	if _, err := CustomNode(10001, " ", []Segment{Text("hi")}); err == nil {
		t.Fatal("expected error for empty nickname")
	}
	if _, err := CustomNode(10001, "Alice", nil); err == nil {
		t.Fatal("expected error for empty content")
	}
	if _, err := RefNode(0); err == nil {
		t.Fatal("expected error for invalid reference id")
	}
	ref, err := RefNode(42)
	if err != nil {
		t.Fatalf("ref node: %v", err)
	}
	if ref.Data["id"] != "42" {
		t.Fatalf("ref id = %q", ref.Data["id"])
	}
}

func TestForwardConstructorsCloneNodes(t *testing.T) {
	// 验证转发动作深复制节点，修改原始切片不影响参数。
	nodes := []Segment{CustomNodeMust(10001, "A", []Segment{Text("one")})}
	action, err := NewSendGroupForward(456, nodes)
	if err != nil {
		t.Fatalf("new forward: %v", err)
	}
	nodes[0].Data["nickname"] = "MUTATED"
	stored, ok := action.Params["messages"].([]Segment)
	if !ok {
		t.Fatal("messages param missing")
	}
	if stored[0].Data["nickname"] != "A" {
		t.Fatalf("params were mutated: %+v", stored[0])
	}
	if _, err := NewSendGroupForward(456, nil); err == nil {
		t.Fatal("expected error for empty nodes")
	}
}

func TestQueryConstructorsBuildParams(t *testing.T) {
	// 验证查询类动作的参数构造。
	member, err := NewGetGroupMemberInfo(456, 789, true)
	if err != nil {
		t.Fatalf("new member info: %v", err)
	}
	if member.Params["no_cache"] != true {
		t.Fatalf("params = %+v", member.Params)
	}
	if _, err := NewGetGroupHonorInfo(456, ""); err == nil {
		t.Fatal("expected error for empty honor type")
	}
	if _, err := NewGetRecord("f1", ""); err == nil {
		t.Fatal("expected error for empty out format")
	}
	restart, err := NewSetRestart(500)
	if err != nil {
		t.Fatalf("new restart: %v", err)
	}
	if restart.Params["delay"] != int64(500) {
		t.Fatalf("delay = %v", restart.Params["delay"])
	}
	if _, err := NewSetRestart(-1); err == nil {
		t.Fatal("expected error for negative delay")
	}
}

func TestSpecialSegmentConstructors(t *testing.T) {
	// 验证特殊段构造器。
	if _, err := Share("", "title", ""); err == nil {
		t.Fatal("expected error for empty url")
	}
	share, err := Share("https://example.com", "title", "desc")
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if share.Type != SegShare || share.Data["url"] == "" || share.Data["content"] != "desc" {
		t.Fatalf("share = %+v", share)
	}
	if JSONData(`{"a":1}`).Data["data"] != `{"a":1}` {
		t.Fatal("json data mismatch")
	}
	if XMLData("<x/>").Type != SegXML {
		t.Fatal("xml type mismatch")
	}
	if CardImage("file.png").Data["file"] != "file.png" {
		t.Fatal("cardimage file mismatch")
	}
	if Video("v.mp4").Type != SegVideo {
		t.Fatal("video type mismatch")
	}
	if Forward("res-1").Data["id"] != "res-1" {
		t.Fatal("forward id mismatch")
	}
}

func TestNewSendMessageCloneSegments(t *testing.T) {
	// 验证 send_msg 深复制消息段。
	segments := []Segment{Text("hello")}
	action, err := NewSendMessage(TypePrivate, 10001, segments)
	if err != nil {
		t.Fatalf("new send message: %v", err)
	}
	segments[0].Data["text"] = "mutated"
	stored, ok := action.Params["message"].([]Segment)
	if !ok {
		t.Fatal("message param missing")
	}
	if PlainText(stored) != "hello" {
		t.Fatalf("params were mutated: %q", PlainText(stored))
	}
}

// CustomNodeMust is a test helper that panics on invalid input.
// CustomNodeMust 是非法输入即 panic 的测试辅助。
func CustomNodeMust(from UserID, nickname string, content []Segment) Segment {
	node, err := CustomNode(from, nickname, content)
	if err != nil {
		panic(err)
	}
	return node
}
