// Package domain defines the pure OneBot v11 protocol model.
// Package domain 定义 OneBot v11 协议的纯模型：命名类型、校验构造与业务方法。
// 仅依赖标准库，不使用 JSON 标签；线上编解码由 infrastructure 与 application 负责。
package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// 领域标识与枚举统一使用命名类型。
type (
	BotID       int64
	UserID      int64
	GroupID     int64
	MessageID   int64
	PostType    string
	MessageType string
	SegmentType string
	ActionName  string
)

// 事件大类（post_type）。
const (
	PostMessage PostType = "message"
	PostNotice  PostType = "notice"
	PostRequest PostType = "request"
	PostMeta    PostType = "meta_event"
)

// 消息场景（message_type）。
const (
	TypePrivate MessageType = "private"
	TypeGroup   MessageType = "group"
)

// 常用消息段类型。
const (
	SegText   SegmentType = "text"
	SegAt     SegmentType = "at"
	SegReply  SegmentType = "reply"
	SegImage  SegmentType = "image"
	SegFace   SegmentType = "face"
	SegRecord SegmentType = "record"
)

// 常用动作名。
const (
	ActionSendPrivate  ActionName = "send_private_msg"
	ActionSendGroup    ActionName = "send_group_msg"
	ActionDeleteMsg    ActionName = "delete_msg"
	ActionGetLoginInfo ActionName = "get_login_info"
)

// Segment 是一条消息段。
type Segment struct {
	Type SegmentType
	Data map[string]string
}

// NewSegment 校验并构造消息段。
func NewSegment(t SegmentType, data map[string]string) (Segment, error) {
	if t == "" {
		return Segment{}, fmt.Errorf("onebot: 消息段类型不能为空")
	}
	return Segment{Type: t, Data: data}, nil
}

// clone 深复制消息段，保证领域对象不被外部修改。
func (s Segment) clone() Segment {
	data := make(map[string]string, len(s.Data))
	for k, v := range s.Data {
		data[k] = v
	}
	return Segment{Type: s.Type, Data: data}
}

// CloneSegments 深复制消息段切片。
func CloneSegments(in []Segment) []Segment {
	out := make([]Segment, len(in))
	for i, s := range in {
		out[i] = s.clone()
	}
	return out
}

// Text 构造文本消息段。
func Text(s string) Segment { return Segment{Type: SegText, Data: map[string]string{"text": s}} }

// At 构造 @ 消息段。
func At(id UserID) Segment {
	return Segment{Type: SegAt, Data: map[string]string{"qq": strconv.FormatInt(int64(id), 10)}}
}

// Reply 构造引用消息段。
func Reply(id MessageID) Segment {
	return Segment{Type: SegReply, Data: map[string]string{"id": strconv.FormatInt(int64(id), 10)}}
}

// Image 构造图片消息段。
func Image(file string) Segment {
	return Segment{Type: SegImage, Data: map[string]string{"file": file}}
}

// PlainText 提取消息段中纯文本的拼接结果。
func PlainText(segments []Segment) string {
	var b strings.Builder
	for _, s := range segments {
		if s.Type == SegText {
			b.WriteString(s.Data["text"])
		}
	}
	return b.String()
}

// Action 是一次 OneBot 动作调用。
type Action struct {
	Name   ActionName
	Params map[string]any
}

// NewSendPrivateMessage 构造私聊消息动作；消息段会被深复制。
func NewSendPrivateMessage(to UserID, segments []Segment) (Action, error) {
	if to <= 0 {
		return Action{}, fmt.Errorf("onebot: 私聊目标用户 ID 无效: %d", to)
	}
	if len(segments) == 0 {
		return Action{}, fmt.Errorf("onebot: 消息内容不能为空")
	}
	return Action{Name: ActionSendPrivate, Params: map[string]any{
		"user_id": int64(to),
		"message": CloneSegments(segments),
	}}, nil
}

// NewSendGroupMessage 构造群聊消息动作；消息段会被深复制。
func NewSendGroupMessage(to GroupID, segments []Segment) (Action, error) {
	if to <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", to)
	}
	if len(segments) == 0 {
		return Action{}, fmt.Errorf("onebot: 消息内容不能为空")
	}
	return Action{Name: ActionSendGroup, Params: map[string]any{
		"group_id": int64(to),
		"message":  CloneSegments(segments),
	}}, nil
}

// NewDeleteMessage 构造撤回消息动作。
func NewDeleteMessage(id MessageID) (Action, error) {
	if id == 0 {
		return Action{}, fmt.Errorf("onebot: 消息 ID 无效: %d", id)
	}
	return Action{Name: ActionDeleteMsg, Params: map[string]any{"message_id": int64(id)}}, nil
}

// Sender 是消息发送者快照。
type Sender struct {
	ID       UserID
	Nickname string
	Card     string
}

// Event 是 OneBot 事件的领域模型：已知公共字段给出类型化视图，
// 其余字段统一放入 Extra，由业务上下文按需读取。
type Event struct {
	Time       int64
	SelfID     BotID
	Post       PostType
	Type       MessageType
	SubType    string
	MessageID  MessageID
	UserID     UserID
	GroupID    GroupID
	RawMessage string
	Segments   []Segment
	Sender     Sender
	Extra      map[string]any
}

// IsMessage 判断是否为消息事件。
func (e Event) IsMessage() bool { return e.Post == PostMessage }

// InGroup 判断是否为群聊消息事件。
func (e Event) InGroup() bool { return e.IsMessage() && e.Type == TypeGroup }

// Text 返回事件文本：优先消息段纯文本，回退 raw_message。
func (e Event) Text() string {
	if t := PlainText(e.Segments); t != "" {
		return t
	}
	return e.RawMessage
}
