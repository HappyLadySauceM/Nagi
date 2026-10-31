package builtin

import (
	"context"
	"github.com/HappyLadySauceM/Nagi/internal/onebot/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	pc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

// PublicOptions binds public tools to the source account and delivery ledger.
// PublicOptions 将公开工具绑定到来源账号与投递台账。
type PublicOptions struct {
	Account func(int64) (contracts.PublicAPI, bool)
	Send    func(context.Context, ob.PublicCommand) (ob.PublicResult, error)
	// AuthorizeTarget validates a cross-conversation group before a destructive
	// action runs, so a disabled target cannot be administered.
	// AuthorizeTarget 在破坏性动作执行前校验跨会话群，停用目标不会被操作。
	AuthorizeTarget func(context.Context, string, int64) error
}
type publicProvider struct{ opts PublicOptions }

// NewPublicProvider registers the closed public API catalog.
// NewPublicProvider 注册封闭的公开 API 清单。
func NewPublicProvider(opts PublicOptions) pc.ToolProvider { return publicProvider{opts} }
func (publicProvider) Manifest() pc.Manifest {
	return pc.Manifest{ID: "builtin.qq.public", Version: "1", Description: "OneBot v11 公开 API"}
}
func (publicProvider) Close(context.Context) error { return nil }

type publicSpec struct {
	name        ob.ActionName
	description string
	params      map[string]pc.Parameter
	group       bool
	send        bool
	sensitive   bool
	read        bool
}

func param(t string, required bool) pc.Parameter { return pc.Parameter{Type: t, Required: required} }
func fields(names ...string) map[string]pc.Parameter {
	out := map[string]pc.Parameter{}
	for _, name := range names {
		t := "string"
		required := true
		switch name {
		case "group_id":
			t = "integer"
			required = false
		case "user_id", "message_id":
			t = "integer"
		case "times", "duration", "delay":
			t = "integer"
			required = false
		case "enable", "approve", "no_cache", "reject_add_request", "is_dismiss", "auto_escape":
			t = "boolean"
			required = false
		case "card", "special_title", "remark", "reason", "domain", "anonymous_flag", "flag":
			required = false
		case "message_type":
			required = false
		}
		out[name] = param(t, required)
	}
	return out
}

// required marks the named parameters as mandatory.
// required 把指定参数标记为必填。
func required(params map[string]pc.Parameter, names ...string) map[string]pc.Parameter {
	for _, name := range names {
		if p, ok := params[name]; ok {
			p.Required = true
			params[name] = p
		}
	}
	return params
}

func publicSpecs() []publicSpec {
	message := pc.Parameter{Required: true, Types: []string{"string", "array"}, Items: &pc.Parameter{Type: "object", Properties: map[string]pc.Parameter{"type": param("string", true), "data": param("object", true)}}}
	specs := []publicSpec{
		{name: ob.ActionSendPrivate, description: "发送私聊消息", params: fields("user_id", "auto_escape"), send: true},
		{name: ob.ActionSendGroup, description: "发送群消息", params: fields("group_id", "auto_escape"), group: true, send: true},
		{name: ob.ActionSendMessage, description: "发送消息，省略类型时根据目标 ID 判断；无目标时使用当前会话", params: fields("message_type", "user_id", "group_id", "auto_escape"), send: true},
		{name: ob.ActionDeleteMsg, description: "撤回消息；消息归属未知，需要跨会话权限", params: fields("message_id")},
		{name: ob.ActionGetMessage, description: "获取消息；消息归属未知，需要跨会话权限", params: fields("message_id"), read: true},
		{name: ob.ActionGetForwardMessage, description: "获取合并转发消息；需要跨会话权限", params: fields("id"), read: true},
		{name: ob.ActionSendLike, description: "发送好友赞，默认一次", params: fields("user_id", "times")},
		{name: ob.ActionSetGroupKick, description: "群组踢人，可选择拒绝再次申请", params: fields("group_id", "user_id", "reject_add_request"), group: true},
		{name: ob.ActionSetGroupBan, description: "群成员禁言，默认 1800 秒，0 解除禁言", params: fields("group_id", "user_id", "duration"), group: true},
		{name: ob.ActionSetGroupAnonymousBan, description: "匿名成员禁言，传入 anonymous 对象或 anonymous_flag/flag", params: fields("group_id", "anonymous_flag", "flag", "duration"), group: true},
		{name: ob.ActionSetGroupWholeBan, description: "群组全员禁言，enable 必须显式给出", params: required(fields("group_id", "enable"), "enable"), group: true},
		{name: ob.ActionSetGroupAdmin, description: "设置或取消群管理员，enable 默认 true", params: fields("group_id", "user_id", "enable"), group: true},
		{name: ob.ActionSetGroupAnonymous, description: "允许或关闭群匿名聊天，enable 默认 true", params: fields("group_id", "enable"), group: true},
		{name: ob.ActionSetGroupCard, description: "设置群名片，card 必填，空字符串清除名片", params: required(fields("group_id", "user_id", "card"), "card"), group: true},
		{name: ob.ActionSetGroupName, description: "修改群名称", params: fields("group_id", "group_name"), group: true},
		{name: ob.ActionSetGroupLeave, description: "退出群；不支持解散群", params: fields("group_id", "is_dismiss"), group: true},
		{name: ob.ActionSetGroupSpecialTitle, description: "设置专属头衔，special_title 必填，空字符串清除头衔，duration 默认 -1", params: required(fields("group_id", "user_id", "special_title", "duration"), "special_title"), group: true},
		{name: ob.ActionSetFriendAddRequest, description: "审批好友请求，approve 必须显式给出", params: required(fields("flag", "approve", "remark"), "approve")},
		{name: ob.ActionSetGroupAddRequest, description: "审批加群请求或邀请，approve 必须显式给出", params: required(fields("flag", "sub_type", "type", "approve", "reason"), "approve")},
		{name: ob.ActionGetLoginInfo, description: "获取当前机器人登录信息", params: fields(), read: true},
		{name: ob.ActionGetStrangerInfo, description: "获取陌生人信息", params: fields("user_id", "no_cache"), read: true},
		{name: ob.ActionGetFriendList, description: "获取当前机器人好友列表", params: fields(), read: true},
		{name: ob.ActionGetGroupInfo, description: "获取群基本信息，省略 group_id 时查询当前群", params: fields("group_id", "no_cache"), group: true, read: true},
		{name: ob.ActionGetGroupList, description: "获取当前机器人群列表", params: fields(), read: true},
		{name: ob.ActionGetGroupMemberInfo, description: "获取群成员信息", params: fields("group_id", "user_id", "no_cache"), group: true, read: true},
		{name: ob.ActionGetGroupMemberList, description: "获取群成员列表", params: fields("group_id"), group: true, read: true},
		{name: ob.ActionGetGroupHonorInfo, description: "获取群荣誉信息", params: fields("group_id", "type"), group: true, read: true},
		{name: ob.ActionGetCookies, description: "获取 QQ Cookies（敏感凭证）", params: fields("domain"), sensitive: true, read: true},
		{name: ob.ActionGetCSRFToken, description: "获取 CSRF Token（敏感凭证）", params: fields(), sensitive: true, read: true},
		{name: ob.ActionGetCredentials, description: "获取 QQ Cookies 和 CSRF Token（敏感凭证）", params: fields("domain"), sensitive: true, read: true},
		{name: ob.ActionGetRecord, description: "获取语音并转换格式", params: fields("file", "out_format"), read: true},
		{name: ob.ActionGetImage, description: "获取图片文件信息", params: fields("file"), read: true},
		{name: ob.ActionCanSendImage, description: "检查图片发送能力", params: fields(), read: true},
		{name: ob.ActionCanSendRecord, description: "检查语音发送能力", params: fields(), read: true},
		{name: ob.ActionGetStatus, description: "获取 OneBot 运行状态", params: fields(), read: true},
		{name: ob.ActionGetVersionInfo, description: "获取 OneBot 版本", params: fields(), read: true},
		{name: ob.ActionSetRestart, description: "重启 OneBot，delay 单位毫秒，异步接受不表示完成", params: fields("delay")},
		{name: ob.ActionCleanCache, description: "清理 OneBot 缓存", params: fields()},
	}
	for i := range specs {
		s := &specs[i]
		if s.send {
			s.params["message"] = message
		}
		if s.name == ob.ActionSendMessage {
			p := s.params["user_id"]
			p.Required = false
			s.params["user_id"] = p
			p = s.params["message_type"]
			p.Enum = []string{"private", "group"}
			s.params["message_type"] = p
		}
		if s.name == ob.ActionSetGroupAnonymousBan {
			s.params["anonymous"] = pc.Parameter{Type: "object", Properties: map[string]pc.Parameter{"id": param("integer", true), "name": param("string", true), "flag": param("string", true)}}
		}
		if s.name == ob.ActionSetFriendAddRequest || s.name == ob.ActionSetGroupAddRequest {
			p := s.params["flag"]
			p.Required = true
			s.params["flag"] = p
		}
		if s.name == ob.ActionSetGroupAddRequest {
			p := s.params["sub_type"]
			p.Enum = []string{"add", "invite"}
			p.Required = false
			s.params["sub_type"] = p
			s.params["type"] = p
		}
		if s.name == ob.ActionGetGroupHonorInfo {
			p := s.params["type"]
			p.Enum = []string{"talkative", "performer", "legend", "strong_newbie", "emotion", "all"}
			s.params["type"] = p
		}
		if s.name == ob.ActionGetRecord {
			p := s.params["out_format"]
			p.Enum = []string{"mp3", "amr", "wma", "m4a", "spx", "ogg", "wav", "flac"}
			s.params["out_format"] = p
		}
	}
	return specs
}
func (p publicProvider) Tools(context.Context) ([]pc.RegisteredTool, error) {
	out := []pc.RegisteredTool{}
	legacy := map[ob.ActionName]string{ob.ActionGetGroupInfo: "qq.group.read", ob.ActionGetGroupMemberInfo: "qq.group.member.read", ob.ActionSetGroupWholeBan: "qq.group.admin.wholeban", ob.ActionSetGroupCard: "qq.group.admin.card"}
	for _, s := range publicSpecs() {
		allowed := false
		permission := "qq.api." + string(s.name)
		var defaultAllowed *bool = &allowed
		if old, ok := legacy[s.name]; ok {
			permission = old
			defaultAllowed = nil
		}
		category := pc.CategorySystem
		risk := pc.RiskAdmin
		if s.read {
			risk = pc.RiskRead
		}
		if s.sensitive {
			risk = pc.RiskAdmin
		}
		if s.group {
			category = pc.CategoryGroup
		}
		if s.send {
			category = pc.CategoryReply
			risk = pc.RiskWrite
		}
		d := pc.ToolDescriptor{ID: "qq." + string(s.name), Description: s.description, Parameters: s.params, Permission: permission, DefaultAllowed: defaultAllowed, ReadOnly: s.read, SideEffect: !s.read, RiskLevel: risk, Category: category, Sensitive: s.sensitive, AdditionalPermissions: nil}
		// Group actions belong to group policies; friend actions belong to private
		// policies. Account-wide and generic message tools support both kinds.
		// 群操作只属于群聊策略，好友操作只属于私聊策略；账号级与通用消息工具适用两者。
		if s.group || s.name == ob.ActionSetGroupAddRequest || s.name == ob.ActionGetGroupList {
			d.ScopeKinds = []string{pc.ScopeKindGroup}
		} else if s.name == ob.ActionSendPrivate || s.name == ob.ActionSendLike || s.name == ob.ActionSetFriendAddRequest || s.name == ob.ActionGetFriendList {
			d.ScopeKinds = []string{pc.ScopeKindPrivate}
		}
		if s.group || s.name == ob.ActionSendPrivate || s.name == ob.ActionSendMessage || s.name == ob.ActionSendLike || s.name == ob.ActionGetStrangerInfo || s.name == ob.ActionGetMessage || s.name == ob.ActionDeleteMsg || s.name == ob.ActionGetForwardMessage || s.name == ob.ActionSetFriendAddRequest || s.name == ob.ActionSetGroupAddRequest {
			d.AdditionalPermissions = append(d.AdditionalPermissions, "qq.cross_conversation")
		}
		if s.send {
			d.AdditionalPermissions = append(d.AdditionalPermissions, "qq.content.links", "qq.content.at_all", "qq.content.media")
		}
		out = append(out, pc.RegisteredTool{Descriptor: d, Invoke: func(ctx context.Context, args string) (string, error) { return p.invoke(ctx, s, d, args) }})
	}
	return out, nil
}
