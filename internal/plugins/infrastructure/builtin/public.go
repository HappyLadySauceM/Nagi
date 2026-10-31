package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pa "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	pc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

func (p publicProvider) invoke(ctx context.Context, s publicSpec, d pc.ToolDescriptor, raw string) (string, error) {
	scope, ok := pa.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("public tool: missing scope")
	}
	var args map[string]json.RawMessage
	if len(raw) > 65536 {
		return "error: 参数超过 64 KiB", nil
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil || args == nil {
		return "error: 参数必须为 JSON 对象", nil
	}
	for name, value := range args {
		if _, ok := s.params[name]; !ok {
			return "error: 未知参数 " + name, nil
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "error: 参数不能为 null", nil
		}
	}
	for name, def := range s.params {
		if _, ok := args[name]; def.Required && !ok {
			return "error: 缺少参数 " + name, nil
		}
	}
	c := ob.PublicCommand{Name: s.name, Enable: true, Approve: true, Times: 1, Duration: 1800}
	if s.name == ob.ActionSetGroupSpecialTitle {
		c.Duration = -1
	}
	var message json.RawMessage
	targets := map[string]any{"user_id": &c.UserID,
		"group_id":           &c.GroupID,
		"message_id":         &c.MessageID,
		"id":                 &c.ForwardID,
		"message_type":       &c.MessageType,
		"auto_escape":        &c.AutoEscape,
		"message":            &message,
		"no_cache":           &c.NoCache,
		"times":              &c.Times,
		"duration":           &c.Duration,
		"enable":             &c.Enable,
		"approve":            &c.Approve,
		"reject_add_request": &c.RejectAddRequest,
		"is_dismiss":         &c.Dismiss,
		"card":               &c.Card,
		"group_name":         &c.GroupName,
		"special_title":      &c.Title,
		"anonymous":          &c.Anonymous,
		"flag":               &c.Flag,
		"anonymous_flag":     &c.Flag,
		"sub_type":           &c.SubType,
		"remark":             &c.Remark,
		"reason":             &c.Reason,
		"type":               &c.HonorType,
		"domain":             &c.Domain,
		"file":               &c.File,
		"out_format":         &c.OutFormat,
		"delay":              &c.Delay,
	}
	if s.name == ob.ActionSetGroupAddRequest {
		targets["type"] = &c.SubType
		if _, ok := args["sub_type"]; !ok {
			if _, ok := args["type"]; !ok {
				return "error: 缺少 sub_type 或 type", nil
			}
		}
	}
	for name, value := range args {
		if name == "type" && s.name == ob.ActionSetGroupAddRequest {
			if _, ok := args["sub_type"]; ok {
				continue
			}
		}
		if name == "flag" {
			if _, ok := args["anonymous_flag"]; ok {
				continue
			}
		}
		if err := json.Unmarshal(value, targets[name]); err != nil {
			return "error: 参数类型不正确：" + name, nil
		}
		if def := s.params[name]; len(def.Enum) > 0 {
			var v string
			_ = json.Unmarshal(value, &v)
			found := false
			for _, choice := range def.Enum {
				found = found || v == choice
			}
			if !found {
				return "error: 参数值不支持：" + name, nil
			}
		}
	}
	_, explicitGroup := args["group_id"]
	_, explicitUser := args["user_id"]
	if c.GroupID < 0 || c.UserID < 0 || explicitGroup && c.GroupID == 0 || explicitUser && c.UserID == 0 {
		return "error: 目标 ID 必须为正整数", nil
	}
	if s.group && c.GroupID == 0 {
		if scope.Kind != "group" {
			return "error: 请指定 group_id", nil
		}
		c.GroupID = ob.GroupID(scope.TargetID)
	}
	// Administering another group also crosses conversations; the target must be
	// enabled and not observe-only. Sends are checked before reservation instead.
	// 管理其他群同样跨会话：目标必须已启用且不是仅观察模式；发送类在预留前校验。
	if s.group && !s.read && !s.send && c.GroupID > 0 && int64(c.GroupID) != scope.TargetID && p.opts.AuthorizeTarget != nil {
		if err := p.opts.AuthorizeTarget(ctx, "group", int64(c.GroupID)); err != nil {
			return "", err
		}
	}
	if s.name == ob.ActionSendMessage {
		if c.MessageType == "" {
			if c.UserID > 0 && c.GroupID > 0 {
				return "error: 请明确消息类型", nil
			}
			if c.UserID > 0 {
				c.MessageType = ob.TypePrivate
			} else if c.GroupID > 0 {
				c.MessageType = ob.TypeGroup
			} else {
				c.MessageType = ob.MessageType(scope.Kind)
				if scope.Kind == "group" {
					c.GroupID = ob.GroupID(scope.TargetID)
				} else {
					c.UserID = ob.UserID(scope.TargetID)
				}
			}
		}
		if c.MessageType == ob.TypeGroup && c.GroupID == 0 && scope.Kind == "group" {
			c.GroupID = ob.GroupID(scope.TargetID)
		}
		if c.MessageType == ob.TypePrivate && c.UserID == 0 && scope.Kind == "private" {
			c.UserID = ob.UserID(scope.TargetID)
		}
	}
	cross := false
	if s.group {
		cross = scope.Kind != "group" || int64(c.GroupID) != scope.TargetID
	}
	switch s.name {
	case ob.ActionSendPrivate, ob.ActionSendLike, ob.ActionGetStrangerInfo:
		cross = scope.Kind != "private" || int64(c.UserID) != scope.TargetID
	case ob.ActionSendMessage:
		target := int64(c.UserID)
		if c.MessageType == ob.TypeGroup {
			target = int64(c.GroupID)
		}
		cross = scope.Kind != string(c.MessageType) || scope.TargetID != target
	case ob.ActionGetMessage, ob.ActionDeleteMsg, ob.ActionGetForwardMessage,
		ob.ActionSetFriendAddRequest, ob.ActionSetGroupAddRequest:
		// The flag refers to a request from an arbitrary account or group, so the
		// decision always reaches beyond the source conversation.
		// flag 来自任意账号或群的请求，审批结果必然超出来源会话。
		cross = true
	}
	if cross {
		if err := pa.RequirePermission(ctx, "qq.cross_conversation"); err != nil {
			return "", pa.Denied("需要跨会话权限")
		}
	}
	// The bot must not kick or mute itself; the narrowed tools made the same
	// check, and losing it turns one model mistake into an outage.
	// 机器人不得踢出或禁言自身；收窄工具已有同样检查，缺失会让一次误操作中断服务。
	if (s.name == ob.ActionSetGroupKick || s.name == ob.ActionSetGroupBan) && c.UserID == ob.UserID(scope.BotID) {
		return "error: 不能对机器人自身执行该操作", nil
	}
	// Dissolving a group is irreversible and stays a human decision, matching
	// the narrowed leave tool.
	// 解散群不可逆，仍由人工处理，与收窄的退群工具保持一致。
	if s.name == ob.ActionSetGroupLeave && c.Dismiss {
		return "error: 不支持解散群，请人工处理", nil
	}
	if s.send {
		if len(message) == 0 {
			return "error: 缺少 message", nil
		}
		if err := json.Unmarshal(message, &c.Text); err != nil {
			c.Text = nil
			segments, err := ob.SegmentsFromWire(message)
			if err != nil {
				return "error: message 必须为字符串或消息段数组", nil
			}
			c.Segments = segments
		}
		if _, err := pa.CheckMessageContent(ctx, c); err != nil {
			return "", err
		}
	}
	if _, err := c.Action(); err != nil {
		return "error: 请求参数无效", nil
	}
	if err := pa.RequirePermission(ctx, d.Permission); err != nil {
		return "", err
	}
	var result ob.PublicResult
	var err error
	if s.send {
		if p.opts.Send == nil {
			return "", fmt.Errorf("public tool: sender unavailable")
		}
		result, err = p.opts.Send(ctx, c)
	} else {
		api, ok := p.opts.Account(scope.BotID)
		if !ok {
			return "error: 当前机器人账号不可用", nil
		}
		result, err = api.ExecutePublic(ctx, c)
	}
	if err != nil {
		var denial interface{ ToolStatus() string }
		if errors.As(err, &denial) && denial.ToolStatus() == pa.StatusDenied {
			return "", err
		}
		var rejection interface{ DefiniteFailure() bool }
		if errors.As(err, &rejection) && rejection.DefiniteFailure() {
			return `{"status":"failed","message":"OneBot 明确拒绝了操作"}`, nil
		}
		if !s.read {
			return `{"status":"unknown","message":"操作失败或结果未知，请勿自动重试"}`, nil
		}
		return "", err
	}
	status := "ok"
	if result.Async {
		status = "async"
	}
	out, err := json.Marshal(map[string]any{"status": status, "data": result.Data})
	return string(out), err
}

// WithoutPublicDuplicates retains the original providers' current-only tools.
// WithoutPublicDuplicates 保留原提供方中的当前会话专用工具。
func WithoutPublicDuplicates(provider pc.ToolProvider) pc.ToolProvider {
	return filteredPublic{provider}
}

type filteredPublic struct{ pc.ToolProvider }

func (p filteredPublic) Tools(ctx context.Context) ([]pc.RegisteredTool, error) {
	tools, err := p.ToolProvider.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := []pc.RegisteredTool{}
	for _, tool := range tools {
		switch strings.TrimPrefix(tool.Descriptor.ID, "qq.") {
		case "get_group_info", "get_group_member_info", "set_group_whole_ban", "set_group_card":
			continue
		}
		out = append(out, tool)
	}
	return out, nil
}
