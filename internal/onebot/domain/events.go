package domain

// NoticeType enumerates common notice event categories.
// NoticeType 枚举常见通知事件类别。
type NoticeType string

// RequestType enumerates request event categories.
// RequestType 枚举请求事件类别。
type RequestType string

// MetaEventType enumerates meta event categories.
// MetaEventType 枚举元事件类别。
type MetaEventType string

// GroupRequestSubType enumerates group request sub types for approval.
// GroupRequestSubType 枚举加群请求审批的子类型。
type GroupRequestSubType string

// Common notice_type values from OneBot v11 and mainstream implementations.
// OneBot v11 及主流实现常见的 notice_type 取值。
const (
	NoticeGroupUpload   NoticeType = "group_upload"
	NoticeGroupAdmin    NoticeType = "group_admin"
	NoticeGroupDecrease NoticeType = "group_decrease"
	NoticeGroupIncrease NoticeType = "group_increase"
	NoticeGroupBan      NoticeType = "group_ban"
	NoticeFriendAdd     NoticeType = "friend_add"
	NoticeGroupRecall   NoticeType = "group_recall"
	NoticeFriendRecall  NoticeType = "friend_recall"
	NoticeNotify        NoticeType = "notify"
	NoticeEssence       NoticeType = "essence"
)

// Common request_type values.
// 常见 request_type 取值。
const (
	RequestFriend RequestType = "friend"
	RequestGroup  RequestType = "group"
)

// Common meta_event_type values.
// 常见 meta_event_type 取值。
const (
	MetaLifecycle MetaEventType = "lifecycle"
	MetaHeartbeat MetaEventType = "heartbeat"
)

// Group request sub types for set_group_add_request.
// set_group_add_request 的加群请求子类型。
const (
	GroupRequestAdd    GroupRequestSubType = "add"
	GroupRequestInvite GroupRequestSubType = "invite"
)

// NoticeType returns the notice_type of notice events; empty for other posts.
// NoticeType 返回通知事件的 notice_type；非通知事件返回空。
func (e Event) NoticeType() NoticeType {
	if e.Post != PostNotice {
		return ""
	}
	return NoticeType(e.stringExtra("notice_type"))
}

// RequestType returns the request_type of request events; empty for other posts.
// RequestType 返回请求事件的 request_type；非请求事件返回空。
func (e Event) RequestType() RequestType {
	if e.Post != PostRequest {
		return ""
	}
	return RequestType(e.stringExtra("request_type"))
}

// MetaType returns the meta_event_type of meta events; empty for other posts.
// MetaType 返回元事件的 meta_event_type；非元事件返回空。
func (e Event) MetaType() MetaEventType {
	if e.Post != PostMeta {
		return ""
	}
	return MetaEventType(e.stringExtra("meta_event_type"))
}

// IsHeartbeat reports whether the event is a heartbeat meta event.
// IsHeartbeat 报告事件是否为心跳元事件。
func (e Event) IsHeartbeat() bool {
	return e.Post == PostMeta && e.MetaType() == MetaHeartbeat
}

// Flag returns the approval flag of request events; empty when absent.
// Flag 返回请求事件的审批标识；缺失时返回空。
func (e Event) Flag() string {
	return e.stringExtra("flag")
}

// Comment returns the verification comment of request events; empty when absent.
// Comment 返回请求事件的验证信息；缺失时返回空。
func (e Event) Comment() string {
	return e.stringExtra("comment")
}

// OperatorID returns the operator user id for notices such as bans and recalls.
// OperatorID 返回禁言、撤回等通知中的操作者用户 ID；缺失时返回 0。
func (e Event) OperatorID() UserID {
	return UserID(e.intExtra("operator_id"))
}

// BanDurationSeconds returns the ban duration in seconds for group_ban notices.
// BanDurationSeconds 返回群禁言通知的时长（秒）；缺失时返回 0。
func (e Event) BanDurationSeconds() int64 {
	return e.intExtra("duration")
}

// HeartbeatIntervalMillis returns the heartbeat interval in milliseconds.
// HeartbeatIntervalMillis 返回心跳事件的间隔（毫秒）；缺失时返回 0。
func (e Event) HeartbeatIntervalMillis() int64 {
	if !e.IsHeartbeat() {
		return 0
	}
	return e.intExtra("heartbeat_interval")
}

func (e Event) stringExtra(key string) string {
	value, _ := e.Extra[key].(string)
	return value
}

func (e Event) intExtra(key string) int64 {
	switch value := e.Extra[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}
