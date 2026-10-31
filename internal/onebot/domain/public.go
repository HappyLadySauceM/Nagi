package domain

import "fmt"

// PublicCommand is a typed union of the 38 standard public API requests.
// PublicCommand 是 38 个标准公开 API 请求的类型化联合。
type PublicCommand struct {
	Name             ActionName
	UserID           UserID
	GroupID          GroupID
	MessageID        MessageID
	ForwardID        string
	MessageType      MessageType
	Text             *string
	Segments         []Segment
	AutoEscape       bool
	NoCache          bool
	Times            int
	Duration         int64
	Enable           bool
	RejectAddRequest bool
	Dismiss          bool
	Card             string
	GroupName        string
	Title            string
	Anonymous        map[string]any
	Flag             string
	SubType          GroupRequestSubType
	Approve          bool
	Remark           string
	Reason           string
	HonorType        string
	Domain           string
	File             string
	OutFormat        string
	Delay            int64
}

// PublicResult preserves standard response field names and asynchronous status.
// PublicResult 保留标准响应字段名与异步状态。
type PublicResult struct {
	Data      any
	Async     bool
	MessageID int64
}

// Action validates the closed command union before constructing a wire action.
// Action 在构造线上动作前校验封闭的命令联合。
func (c PublicCommand) Action() (Action, error) {
	var a Action
	var err error
	segments := c.Segments
	if c.Text != nil {
		segments = []Segment{Text(*c.Text)}
	}
	switch c.Name {
	case ActionSendPrivate:
		a, err = NewSendPrivateMessage(c.UserID, segments)
	case ActionSendGroup:
		a, err = NewSendGroupMessage(c.GroupID, segments)
	case ActionSendMessage:
		target := int64(c.UserID)
		if c.MessageType == TypeGroup {
			target = int64(c.GroupID)
		}
		a, err = NewSendMessage(c.MessageType, target, segments)
	case ActionDeleteMsg:
		return NewDeleteMessage(c.MessageID)
	case ActionGetMessage:
		return NewGetMessage(c.MessageID)
	case ActionGetForwardMessage:
		return NewGetForwardMessage(c.ForwardID)
	case ActionSendLike:
		if c.Times < 1 || c.Times > 10 {
			return Action{}, fmt.Errorf("onebot: invalid like count")
		}
		return NewSendLike(c.UserID, c.Times)
	case ActionSetGroupKick:
		return NewSetGroupKick(c.GroupID, c.UserID, c.RejectAddRequest)
	case ActionSetGroupBan:
		return NewSetGroupBan(c.GroupID, c.UserID, c.Duration)
	case ActionSetGroupAnonymousBan:
		if c.Anonymous != nil {
			if c.GroupID <= 0 || c.Duration < 0 {
				return Action{}, fmt.Errorf("onebot: invalid anonymous ban")
			}
			copy := make(map[string]any, len(c.Anonymous))
			for k, v := range c.Anonymous {
				copy[k] = v
			}
			return Action{Name: c.Name, Params: map[string]any{"group_id": int64(c.GroupID), "anonymous": copy, "duration": c.Duration}}, nil
		}
		return NewSetGroupAnonymousBan(c.GroupID, c.Flag, c.Duration)
	case ActionSetGroupWholeBan:
		return NewSetGroupWholeBan(c.GroupID, c.Enable)
	case ActionSetGroupAdmin:
		return NewSetGroupAdmin(c.GroupID, c.UserID, c.Enable)
	case ActionSetGroupAnonymous:
		return NewSetGroupAnonymous(c.GroupID, c.Enable)
	case ActionSetGroupCard:
		return NewSetGroupCard(c.GroupID, c.UserID, c.Card)
	case ActionSetGroupName:
		return NewSetGroupName(c.GroupID, c.GroupName)
	case ActionSetGroupLeave:
		return NewSetGroupLeave(c.GroupID, c.Dismiss)
	case ActionSetGroupSpecialTitle:
		if c.Duration < -1 {
			return Action{}, fmt.Errorf("onebot: invalid title duration")
		}
		a, err = NewSetGroupSpecialTitle(c.GroupID, c.UserID, c.Title)
		if err == nil {
			a.Params["duration"] = c.Duration
		}
		return a, err
	case ActionSetFriendAddRequest:
		return NewSetFriendAddRequest(c.Flag, c.Approve, c.Remark)
	case ActionSetGroupAddRequest:
		return NewSetGroupAddRequest(c.Flag, c.SubType, c.Approve, c.Reason)
	case ActionGetLoginInfo:
		return NewGetLoginInfo()
	case ActionGetStrangerInfo:
		return NewGetStrangerInfo(c.UserID, c.NoCache)
	case ActionGetFriendList:
		return NewGetFriendList()
	case ActionGetGroupInfo:
		return NewGetGroupInfo(c.GroupID, c.NoCache)
	case ActionGetGroupList:
		return Action{Name: c.Name, Params: map[string]any{}}, nil
	case ActionGetGroupMemberInfo:
		return NewGetGroupMemberInfo(c.GroupID, c.UserID, c.NoCache)
	case ActionGetGroupMemberList:
		return NewGetGroupMemberList(c.GroupID)
	case ActionGetGroupHonorInfo:
		return NewGetGroupHonorInfo(c.GroupID, c.HonorType)
	case ActionGetCookies:
		return NewGetCookies(c.Domain)
	case ActionGetCSRFToken:
		return NewGetCSRFToken()
	case ActionGetCredentials:
		return NewGetCredentials(c.Domain)
	case ActionGetRecord:
		return NewGetRecord(c.File, c.OutFormat)
	case ActionGetImage:
		return NewGetImage(c.File)
	case ActionCanSendImage:
		return NewCanSendImage()
	case ActionCanSendRecord:
		return NewCanSendRecord()
	case ActionGetStatus:
		return NewGetStatus()
	case ActionGetVersionInfo:
		return NewGetVersionInfo()
	case ActionSetRestart:
		return NewSetRestart(c.Delay)
	case ActionCleanCache:
		return NewCleanCache()
	default:
		return Action{}, fmt.Errorf("onebot: unsupported public command")
	}
	if err != nil {
		return Action{}, err
	}
	if c.Text != nil {
		if *c.Text == "" {
			return Action{}, fmt.Errorf("onebot: empty message")
		}
		a.Params["message"] = *c.Text
		a.Params["auto_escape"] = c.AutoEscape
	} else {
		if len(c.Segments) == 0 {
			return Action{}, fmt.Errorf("onebot: empty message")
		}
		a.Params["message"] = CloneSegments(c.Segments)
	}
	return a, nil
}
