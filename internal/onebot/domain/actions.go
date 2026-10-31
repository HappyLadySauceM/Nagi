package domain

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Additional OneBot v11 standard action names.
// OneBot v11 规范的其余标准动作名。
const (
	ActionSendMessage          ActionName = "send_msg"
	ActionGetMessage           ActionName = "get_msg"
	ActionGetForwardMessage    ActionName = "get_forward_msg"
	ActionSendPrivateForward   ActionName = "send_private_forward_msg"
	ActionSendGroupForward     ActionName = "send_group_forward_msg"
	ActionSendLike             ActionName = "send_like"
	ActionSetGroupKick         ActionName = "set_group_kick"
	ActionSetGroupBan          ActionName = "set_group_ban"
	ActionSetGroupAnonymousBan ActionName = "set_group_anonymous_ban"
	ActionSetGroupWholeBan     ActionName = "set_group_whole_ban"
	ActionSetGroupAdmin        ActionName = "set_group_admin"
	ActionSetGroupAnonymous    ActionName = "set_group_anonymous"
	ActionSetGroupCard         ActionName = "set_group_card"
	ActionSetGroupName         ActionName = "set_group_name"
	ActionSetGroupLeave        ActionName = "set_group_leave"
	ActionSetGroupSpecialTitle ActionName = "set_group_special_title"
	ActionSetFriendAddRequest  ActionName = "set_friend_add_request"
	ActionSetGroupAddRequest   ActionName = "set_group_add_request"
	ActionGetStrangerInfo      ActionName = "get_stranger_info"
	ActionGetFriendList        ActionName = "get_friend_list"
	ActionGetGroupInfo         ActionName = "get_group_info"
	ActionGetGroupList         ActionName = "get_group_list"
	ActionGetGroupMemberInfo   ActionName = "get_group_member_info"
	ActionGetGroupMemberList   ActionName = "get_group_member_list"
	ActionGetGroupHonorInfo    ActionName = "get_group_honor_info"
	ActionGetCookies           ActionName = "get_cookies"
	ActionGetCSRFToken         ActionName = "get_csrf_token"
	ActionGetCredentials       ActionName = "get_credentials"
	ActionGetRecord            ActionName = "get_record"
	ActionGetImage             ActionName = "get_image"
	ActionCanSendImage         ActionName = "can_send_image"
	ActionCanSendRecord        ActionName = "can_send_record"
	ActionGetStatus            ActionName = "get_status"
	ActionGetVersionInfo       ActionName = "get_version_info"
	ActionSetRestart           ActionName = "set_restart"
	ActionCleanCache           ActionName = "clean_cache"
)

// Additional standard and widely supported segment types.
// 其余标准与广泛支持的消息段类型。
const (
	SegShare     SegmentType = "share"
	SegVideo     SegmentType = "video"
	SegJSON      SegmentType = "json"
	SegXML       SegmentType = "xml"
	SegCardImage SegmentType = "cardimage"
	SegNode      SegmentType = "node"
	SegForward   SegmentType = "forward"
)

// Share builds a link-share segment; url and title are required.
// Share 构造链接分享段；url 与 title 必填。
func Share(url, title, content string) (Segment, error) {
	if strings.TrimSpace(url) == "" {
		return Segment{}, fmt.Errorf("onebot: 分享链接不能为空")
	}
	if strings.TrimSpace(title) == "" {
		return Segment{}, fmt.Errorf("onebot: 分享标题不能为空")
	}
	data := map[string]string{"url": url, "title": title}
	if content != "" {
		data["content"] = content
	}
	return Segment{Type: SegShare, Data: data}, nil
}

// JSONData builds a raw JSON message segment.
// JSONData 构造原始 JSON 消息段。
func JSONData(payload string) Segment {
	return Segment{Type: SegJSON, Data: map[string]string{"data": payload}}
}

// XMLData builds a raw XML message segment.
// XMLData 构造原始 XML 消息段。
func XMLData(payload string) Segment {
	return Segment{Type: SegXML, Data: map[string]string{"data": payload}}
}

// CardImage builds a rich card image segment.
// CardImage 构造卡片图片段。
func CardImage(file string) Segment {
	return Segment{Type: SegCardImage, Data: map[string]string{"file": file}}
}

// Video builds a video segment.
// Video 构造视频段。
func Video(file string) Segment {
	return Segment{Type: SegVideo, Data: map[string]string{"file": file}}
}

// Forward builds a received merged-forward reference segment.
// Forward 构造合并转发的引用段（接收侧展示用）。
func Forward(id string) Segment {
	return Segment{Type: SegForward, Data: map[string]string{"id": id}}
}

// RefNode builds a forward node referencing an existing message.
// RefNode 构造引用已有消息的合并转发节点。
func RefNode(id MessageID) (Segment, error) {
	if id <= 0 {
		return Segment{}, fmt.Errorf("onebot: 节点消息 ID 无效: %d", id)
	}
	return Segment{Type: SegNode, Data: map[string]string{
		"id": strconv.FormatInt(int64(id), 10),
	}}, nil
}

// CustomNode builds a forward node with a custom sender and content.
// CustomNode 构造自定义发送者与内容的合并转发节点。
// Content is embedded as wire-shaped JSON and restored to a real array on send.
// content 以线上形状 JSON 内嵌，发送时还原为真实数组。
func CustomNode(from UserID, nickname string, content []Segment) (Segment, error) {
	if from <= 0 {
		return Segment{}, fmt.Errorf("onebot: 节点用户 ID 无效: %d", from)
	}
	if strings.TrimSpace(nickname) == "" {
		return Segment{}, fmt.Errorf("onebot: 节点昵称不能为空")
	}
	if len(content) == 0 {
		return Segment{}, fmt.Errorf("onebot: 节点内容不能为空")
	}
	payload, err := json.Marshal(WireSegments(CloneSegments(content)))
	if err != nil {
		return Segment{}, fmt.Errorf("onebot: 编码节点内容失败: %w", err)
	}
	return Segment{Type: SegNode, Data: map[string]string{
		"user_id":  strconv.FormatInt(int64(from), 10),
		"nickname": nickname,
		"content":  string(payload),
	}}, nil
}

// NewSendMessage sends a message with an explicit message type and target.
// NewSendMessage 以显式消息类型发送消息（send_msg）。
func NewSendMessage(mt MessageType, target int64, segments []Segment) (Action, error) {
	if len(segments) == 0 {
		return Action{}, fmt.Errorf("onebot: 消息内容不能为空")
	}
	switch mt {
	case TypePrivate:
		if target <= 0 {
			return Action{}, fmt.Errorf("onebot: 私聊目标用户 ID 无效: %d", target)
		}
		return Action{Name: ActionSendMessage, Params: map[string]any{
			"message_type": string(mt),
			"user_id":      target,
			"message":      CloneSegments(segments),
		}}, nil
	case TypeGroup:
		if target <= 0 {
			return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", target)
		}
		return Action{Name: ActionSendMessage, Params: map[string]any{
			"message_type": string(mt),
			"group_id":     target,
			"message":      CloneSegments(segments),
		}}, nil
	default:
		return Action{}, fmt.Errorf("onebot: 未知消息类型: %s", mt)
	}
}

// NewGetMessage fetches one message by id.
// NewGetMessage 按消息 ID 获取消息（get_msg）。
func NewGetMessage(id MessageID) (Action, error) {
	if id == 0 {
		return Action{}, fmt.Errorf("onebot: 消息 ID 无效: %d", id)
	}
	return Action{Name: ActionGetMessage, Params: map[string]any{"message_id": int64(id)}}, nil
}

// NewGetForwardMessage fetches a merged forward message by its string resource id.
// NewGetForwardMessage 按字符串资源 ID 获取合并转发内容（get_forward_msg）。
func NewGetForwardMessage(id string) (Action, error) {
	if strings.TrimSpace(id) == "" {
		return Action{}, fmt.Errorf("onebot: empty forward id")
	}
	return Action{Name: ActionGetForwardMessage, Params: map[string]any{"id": id}}, nil
}

// NewSendPrivateForward sends merged forward nodes in private chat.
// NewSendPrivateForward 在私聊发送合并转发节点。
func NewSendPrivateForward(to UserID, nodes []Segment) (Action, error) {
	if to <= 0 {
		return Action{}, fmt.Errorf("onebot: 私聊目标用户 ID 无效: %d", to)
	}
	if len(nodes) == 0 {
		return Action{}, fmt.Errorf("onebot: 转发节点不能为空")
	}
	return Action{Name: ActionSendPrivateForward, Params: map[string]any{
		"user_id":  int64(to),
		"messages": CloneSegments(nodes),
	}}, nil
}

// NewSendGroupForward sends merged forward nodes in group chat.
// NewSendGroupForward 在群聊发送合并转发节点。
func NewSendGroupForward(to GroupID, nodes []Segment) (Action, error) {
	if to <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", to)
	}
	if len(nodes) == 0 {
		return Action{}, fmt.Errorf("onebot: 转发节点不能为空")
	}
	return Action{Name: ActionSendGroupForward, Params: map[string]any{
		"group_id": int64(to),
		"messages": CloneSegments(nodes),
	}}, nil
}

// NewSendLike sends likes to a friend; times is clamped into 1..10 (0 becomes 1).
// NewSendLike 给好友点赞；次数收敛到 1..10（0 视为 1）。
func NewSendLike(to UserID, times int) (Action, error) {
	if to <= 0 {
		return Action{}, fmt.Errorf("onebot: 目标用户 ID 无效: %d", to)
	}
	if times <= 0 {
		times = 1
	}
	if times > 10 {
		times = 10
	}
	return Action{Name: ActionSendLike, Params: map[string]any{
		"user_id": int64(to),
		"times":   times,
	}}, nil
}

// NewSetGroupKick removes a member; rejectAddRequest also rejects re-entry.
// NewSetGroupKick 移出群成员；rejectAddRequest 同时拒绝再次申请。
func NewSetGroupKick(groupID GroupID, userID UserID, rejectAddRequest bool) (Action, error) {
	if groupID <= 0 || userID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 或用户 ID 无效: %d/%d", groupID, userID)
	}
	return Action{Name: ActionSetGroupKick, Params: map[string]any{
		"group_id":           int64(groupID),
		"user_id":            int64(userID),
		"reject_add_request": rejectAddRequest,
	}}, nil
}

// NewSetGroupBan bans a member for the given seconds; 0 lifts the ban.
// NewSetGroupBan 禁言指定秒数；0 表示解除禁言。
func NewSetGroupBan(groupID GroupID, userID UserID, durationSeconds int64) (Action, error) {
	if groupID <= 0 || userID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 或用户 ID 无效: %d/%d", groupID, userID)
	}
	if durationSeconds < 0 {
		return Action{}, fmt.Errorf("onebot: 禁言时长不能为负: %d", durationSeconds)
	}
	return Action{Name: ActionSetGroupBan, Params: map[string]any{
		"group_id": int64(groupID),
		"user_id":  int64(userID),
		"duration": durationSeconds,
	}}, nil
}

// NewSetGroupAnonymousBan bans an anonymous member by flag.
// NewSetGroupAnonymousBan 按标识禁言匿名成员。
func NewSetGroupAnonymousBan(groupID GroupID, flag string, durationSeconds int64) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	if strings.TrimSpace(flag) == "" {
		return Action{}, fmt.Errorf("onebot: 匿名标识不能为空")
	}
	if durationSeconds < 0 {
		return Action{}, fmt.Errorf("onebot: 禁言时长不能为负: %d", durationSeconds)
	}
	return Action{Name: ActionSetGroupAnonymousBan, Params: map[string]any{
		"group_id":       int64(groupID),
		"anonymous_flag": flag,
		"duration":       durationSeconds,
	}}, nil
}

// NewSetGroupWholeBan enables or disables whole-group mute.
// NewSetGroupWholeBan 开启或关闭全员禁言。
func NewSetGroupWholeBan(groupID GroupID, enable bool) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	return Action{Name: ActionSetGroupWholeBan, Params: map[string]any{
		"group_id": int64(groupID),
		"enable":   enable,
	}}, nil
}

// NewSetGroupAdmin grants or revokes group admin.
// NewSetGroupAdmin 设置或取消群管理员。
func NewSetGroupAdmin(groupID GroupID, userID UserID, enable bool) (Action, error) {
	if groupID <= 0 || userID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 或用户 ID 无效: %d/%d", groupID, userID)
	}
	return Action{Name: ActionSetGroupAdmin, Params: map[string]any{
		"group_id": int64(groupID),
		"user_id":  int64(userID),
		"enable":   enable,
	}}, nil
}

// NewSetGroupAnonymous enables or disables group anonymity.
// NewSetGroupAnonymous 开启或关闭群匿名功能。
func NewSetGroupAnonymous(groupID GroupID, enable bool) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	return Action{Name: ActionSetGroupAnonymous, Params: map[string]any{
		"group_id": int64(groupID),
		"enable":   enable,
	}}, nil
}

// NewSetGroupCard sets a member's group card; empty card clears it.
// NewSetGroupCard 设置群成员名片；空名片表示删除。
func NewSetGroupCard(groupID GroupID, userID UserID, card string) (Action, error) {
	if groupID <= 0 || userID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 或用户 ID 无效: %d/%d", groupID, userID)
	}
	return Action{Name: ActionSetGroupCard, Params: map[string]any{
		"group_id": int64(groupID),
		"user_id":  int64(userID),
		"card":     card,
	}}, nil
}

// NewSetGroupName renames a group.
// NewSetGroupName 设置群名。
func NewSetGroupName(groupID GroupID, name string) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	if strings.TrimSpace(name) == "" {
		return Action{}, fmt.Errorf("onebot: 群名不能为空")
	}
	return Action{Name: ActionSetGroupName, Params: map[string]any{
		"group_id":   int64(groupID),
		"group_name": name,
	}}, nil
}

// NewSetGroupLeave leaves a group; dismiss requires owner privilege.
// NewSetGroupLeave 退出群聊；dismiss 为群主解散。
func NewSetGroupLeave(groupID GroupID, dismiss bool) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	return Action{Name: ActionSetGroupLeave, Params: map[string]any{
		"group_id":   int64(groupID),
		"is_dismiss": dismiss,
	}}, nil
}

// NewSetGroupSpecialTitle sets a member's special title; empty clears it.
// NewSetGroupSpecialTitle 设置专属头衔；空表示移除。
func NewSetGroupSpecialTitle(groupID GroupID, userID UserID, title string) (Action, error) {
	if groupID <= 0 || userID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 或用户 ID 无效: %d/%d", groupID, userID)
	}
	return Action{Name: ActionSetGroupSpecialTitle, Params: map[string]any{
		"group_id":      int64(groupID),
		"user_id":       int64(userID),
		"special_title": title,
		"duration":      int64(-1),
	}}, nil
}

// NewSetFriendAddRequest approves or rejects a friend request.
// NewSetFriendAddRequest 处理好友添加请求。
func NewSetFriendAddRequest(flag string, approve bool, remark string) (Action, error) {
	if strings.TrimSpace(flag) == "" {
		return Action{}, fmt.Errorf("onebot: 请求标识不能为空")
	}
	return Action{Name: ActionSetFriendAddRequest, Params: map[string]any{
		"flag":    flag,
		"approve": approve,
		"remark":  remark,
	}}, nil
}

// NewSetGroupAddRequest approves or rejects a group join request or invitation.
// NewSetGroupAddRequest 处理加群请求或邀请。
func NewSetGroupAddRequest(flag string, subType GroupRequestSubType, approve bool, reason string) (Action, error) {
	if strings.TrimSpace(flag) == "" {
		return Action{}, fmt.Errorf("onebot: 请求标识不能为空")
	}
	switch subType {
	case GroupRequestAdd, GroupRequestInvite:
	default:
		return Action{}, fmt.Errorf("onebot: 未知加群请求子类型: %s", subType)
	}
	return Action{Name: ActionSetGroupAddRequest, Params: map[string]any{
		"flag":     flag,
		"sub_type": string(subType),
		"approve":  approve,
		"reason":   reason,
	}}, nil
}

// NewGetLoginInfo queries the bot account profile.
// NewGetLoginInfo 查询机器人账号信息。
func NewGetLoginInfo() (Action, error) {
	return Action{Name: ActionGetLoginInfo, Params: map[string]any{}}, nil
}

// NewGetStrangerInfo queries a stranger's profile.
// NewGetStrangerInfo 查询陌生人信息。
func NewGetStrangerInfo(id UserID, noCache bool) (Action, error) {
	if id <= 0 {
		return Action{}, fmt.Errorf("onebot: 用户 ID 无效: %d", id)
	}
	return Action{Name: ActionGetStrangerInfo, Params: map[string]any{
		"user_id":  int64(id),
		"no_cache": noCache,
	}}, nil
}

// NewGetFriendList fetches the friend list.
// NewGetFriendList 获取好友列表。
func NewGetFriendList() (Action, error) {
	return Action{Name: ActionGetFriendList, Params: map[string]any{}}, nil
}

// NewGetGroupInfo queries one group; no_cache skips the cached copy.
// NewGetGroupInfo 查询群信息；no_cache 跳过缓存。
func NewGetGroupInfo(id GroupID, noCache bool) (Action, error) {
	if id <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", id)
	}
	return Action{Name: ActionGetGroupInfo, Params: map[string]any{
		"group_id": int64(id),
		"no_cache": noCache,
	}}, nil
}

// NewGetGroupList fetches the joined group list.
// NewGetGroupList 获取已加入的群列表。
func NewGetGroupList(noCache bool) (Action, error) {
	return Action{Name: ActionGetGroupList, Params: map[string]any{"no_cache": noCache}}, nil
}

// NewGetGroupMemberInfo queries one group member.
// NewGetGroupMemberInfo 查询群成员信息。
func NewGetGroupMemberInfo(groupID GroupID, userID UserID, noCache bool) (Action, error) {
	if groupID <= 0 || userID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 或用户 ID 无效: %d/%d", groupID, userID)
	}
	return Action{Name: ActionGetGroupMemberInfo, Params: map[string]any{
		"group_id": int64(groupID),
		"user_id":  int64(userID),
		"no_cache": noCache,
	}}, nil
}

// NewGetGroupMemberList fetches a group's member list.
// NewGetGroupMemberList 获取群成员列表。
func NewGetGroupMemberList(groupID GroupID) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	return Action{Name: ActionGetGroupMemberList, Params: map[string]any{"group_id": int64(groupID)}}, nil
}

// NewGetGroupHonorInfo queries group honor; type is the honor category.
// NewGetGroupHonorInfo 查询群荣誉；type 为荣誉类别。
func NewGetGroupHonorInfo(groupID GroupID, honorType string) (Action, error) {
	if groupID <= 0 {
		return Action{}, fmt.Errorf("onebot: 群 ID 无效: %d", groupID)
	}
	if strings.TrimSpace(honorType) == "" {
		return Action{}, fmt.Errorf("onebot: 荣誉类别不能为空")
	}
	return Action{Name: ActionGetGroupHonorInfo, Params: map[string]any{
		"group_id": int64(groupID),
		"type":     honorType,
	}}, nil
}

// NewGetCookies queries cookies for a domain; empty means all domains.
// NewGetCookies 查询指定域名的 Cookies；空表示全部域名。
func NewGetCookies(domainName string) (Action, error) {
	return Action{Name: ActionGetCookies, Params: map[string]any{"domain": domainName}}, nil
}

// NewGetCSRFToken queries the CSRF token.
// NewGetCSRFToken 查询 CSRF Token。
func NewGetCSRFToken() (Action, error) {
	return Action{Name: ActionGetCSRFToken, Params: map[string]any{}}, nil
}

// NewGetCredentials queries cookies and the CSRF token together.
// NewGetCredentials 同时查询 Cookies 与 CSRF Token。
func NewGetCredentials(domainName string) (Action, error) {
	return Action{Name: ActionGetCredentials, Params: map[string]any{"domain": domainName}}, nil
}

// NewGetRecord fetches a voice file converted to the given format.
// NewGetRecord 获取语音并转换到指定格式。
func NewGetRecord(file, outFormat string) (Action, error) {
	if strings.TrimSpace(file) == "" {
		return Action{}, fmt.Errorf("onebot: 语音文件标识不能为空")
	}
	if strings.TrimSpace(outFormat) == "" {
		return Action{}, fmt.Errorf("onebot: 目标格式不能为空")
	}
	return Action{Name: ActionGetRecord, Params: map[string]any{
		"file":       file,
		"out_format": outFormat,
	}}, nil
}

// NewGetImage fetches an image file by its identifier.
// NewGetImage 按文件标识获取图片。
func NewGetImage(file string) (Action, error) {
	if strings.TrimSpace(file) == "" {
		return Action{}, fmt.Errorf("onebot: 图片文件标识不能为空")
	}
	return Action{Name: ActionGetImage, Params: map[string]any{"file": file}}, nil
}

// NewCanSendImage probes image capability.
// NewCanSendImage 探测图片发送能力。
func NewCanSendImage() (Action, error) {
	return Action{Name: ActionCanSendImage, Params: map[string]any{}}, nil
}

// NewCanSendRecord probes voice capability.
// NewCanSendRecord 探测语音发送能力。
func NewCanSendRecord() (Action, error) {
	return Action{Name: ActionCanSendRecord, Params: map[string]any{}}, nil
}

// NewGetStatus queries the runtime status of the OneBot implementation.
// NewGetStatus 查询 OneBot 实现的运行状态。
func NewGetStatus() (Action, error) {
	return Action{Name: ActionGetStatus, Params: map[string]any{}}, nil
}

// NewGetVersionInfo queries the OneBot implementation version.
// NewGetVersionInfo 查询 OneBot 实现的版本信息。
func NewGetVersionInfo() (Action, error) {
	return Action{Name: ActionGetVersionInfo, Params: map[string]any{}}, nil
}

// NewSetRestart restarts the OneBot implementation after the given delay.
// NewSetRestart 在指定延时后重启 OneBot 实现。
func NewSetRestart(delayMillis int64) (Action, error) {
	if delayMillis < 0 {
		return Action{}, fmt.Errorf("onebot: 重启延时不能为负: %d", delayMillis)
	}
	return Action{Name: ActionSetRestart, Params: map[string]any{"delay": delayMillis}}, nil
}

// NewCleanCache clears the implementation's cache.
// NewCleanCache 清理实现侧缓存。
func NewCleanCache() (Action, error) {
	return Action{Name: ActionCleanCache, Params: map[string]any{}}, nil
}
