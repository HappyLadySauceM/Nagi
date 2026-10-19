package domain

// MemberRole enumerates group member roles.
// MemberRole 枚举群成员角色。
type MemberRole string

// Common member roles.
// 常见成员角色。
const (
	RoleOwner  MemberRole = "owner"
	RoleAdmin  MemberRole = "admin"
	RoleMember MemberRole = "member"
)

// LoginInfo is the result of get_login_info.
// LoginInfo 是 get_login_info 的结果。
type LoginInfo struct {
	UserID   UserID
	Nickname string
}

// StrangerInfo is the result of get_stranger_info.
// StrangerInfo 是 get_stranger_info 的结果。
type StrangerInfo struct {
	UserID   UserID
	Nickname string
	Sex      string
	Age      int64
	Extra    map[string]any
}

// FriendInfo is one entry of get_friend_list.
// FriendInfo 是 get_friend_list 的条目。
type FriendInfo struct {
	UserID   UserID
	Nickname string
	Remark   string
}

// GroupInfo is the result of get_group_info and one entry of get_group_list.
// GroupInfo 是 get_group_info 的结果与 get_group_list 的条目。
type GroupInfo struct {
	GroupID        GroupID
	GroupName      string
	MemberCount    int64
	MaxMemberCount int64
}

// GroupMemberInfo is the result of get_group_member_info and entries of
// get_group_member_list.
// GroupMemberInfo 是 get_group_member_info 的结果与 get_group_member_list 的条目。
type GroupMemberInfo struct {
	GroupID      GroupID
	UserID       UserID
	Nickname     string
	Card         string
	Role         MemberRole
	JoinTime     int64
	LastSentTime int64
	Extra        map[string]any
}

// MessageResult is the result of get_msg.
// MessageResult 是 get_msg 的结果。
type MessageResult struct {
	Time       int64
	MessageID  MessageID
	UserID     UserID
	GroupID    GroupID
	Segments   []Segment
	RawMessage string
	Extra      map[string]any
}

// ForwardNode is one node of get_forward_msg results.
// ForwardNode 是 get_forward_msg 结果中的节点。
type ForwardNode struct {
	UserID   UserID
	Nickname string
	Time     int64
	Segments []Segment
}

// ForwardReceipt is the result of send_private_forward_msg / send_group_forward_msg.
// ForwardReceipt 是合并转发动作的结果。
type ForwardReceipt struct {
	MessageID MessageID
	ResID     string
}

// FileResult is the result of get_record / get_image.
// FileResult 是 get_record / get_image 的结果。
type FileResult struct {
	File     string
	Filename string
	URL      string
	Size     int64
}

// BoolCapability is the result of can_send_image / can_send_record.
// BoolCapability 是 can_send_image / can_send_record 的结果。
type BoolCapability struct {
	Supported bool
}

// RuntimeStatus is the result of get_status.
// RuntimeStatus 是 get_status 的结果。
type RuntimeStatus struct {
	Online bool
	Good   bool
	Extra  map[string]any
}

// VersionInfo is the result of get_version_info.
// VersionInfo 是 get_version_info 的结果。
type VersionInfo struct {
	AppName         string
	AppVersion      string
	ProtocolVersion string
	Extra           map[string]any
}

// CookiesResult is the result of get_cookies.
// CookiesResult 是 get_cookies 的结果。
type CookiesResult struct {
	Cookies string
}

// CSRFTokenResult is the result of get_csrf_token.
// CSRFTokenResult 是 get_csrf_token 的结果。
type CSRFTokenResult struct {
	Token int64
}

// CredentialsResult is the result of get_credentials.
// CredentialsResult 是 get_credentials 的结果。
type CredentialsResult struct {
	Cookies   string
	CSRFToken int64
}

// HonorInfo is the result of get_group_honor_info.
// HonorInfo 是 get_group_honor_info 的结果。
type HonorInfo struct {
	GroupID GroupID
	Type    string
	Extra   map[string]any
}
