package application

import (
	"context"
	"encoding/json"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// GetLoginInfo implements contracts.InfoReader.
// GetLoginInfo 实现 contracts.InfoReader（get_login_info）。
func (s *ClientService) GetLoginInfo(ctx context.Context) (domain.LoginInfo, error) {
	var out domain.LoginInfo
	action, err := domain.NewGetLoginInfo()
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.LoginInfo{UserID: domain.UserID(row.UserID), Nickname: row.Nickname}, nil
}

// GetStrangerInfo implements contracts.InfoReader.
// GetStrangerInfo 实现 contracts.InfoReader（get_stranger_info）。
func (s *ClientService) GetStrangerInfo(ctx context.Context, id domain.UserID, noCache bool) (domain.StrangerInfo, error) {
	var out domain.StrangerInfo
	action, err := domain.NewGetStrangerInfo(id, noCache)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
		Sex      string `json:"sex"`
		Age      int64  `json:"age"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.StrangerInfo{
		UserID:   domain.UserID(row.UserID),
		Nickname: row.Nickname,
		Sex:      row.Sex,
		Age:      row.Age,
		Extra:    decodeExtra(payload, "user_id", "nickname", "sex", "age"),
	}, nil
}

// GetFriendList implements contracts.InfoReader.
// GetFriendList 实现 contracts.InfoReader（get_friend_list）。
func (s *ClientService) GetFriendList(ctx context.Context) ([]domain.FriendInfo, error) {
	action, err := domain.NewGetFriendList()
	if err != nil {
		return nil, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
		Remark   string `json:"remark"`
	}
	if err := decodeJSON(payload, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.FriendInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.FriendInfo{
			UserID:   domain.UserID(row.UserID),
			Nickname: row.Nickname,
			Remark:   row.Remark,
		})
	}
	return out, nil
}

// GetGroupInfo implements contracts.InfoReader.
// GetGroupInfo 实现 contracts.InfoReader（get_group_info）。
func (s *ClientService) GetGroupInfo(ctx context.Context, id domain.GroupID, noCache bool) (domain.GroupInfo, error) {
	var out domain.GroupInfo
	action, err := domain.NewGetGroupInfo(id, noCache)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	return decodeGroupInfo(payload)
}

// GetGroupList implements contracts.InfoReader.
// GetGroupList 实现 contracts.InfoReader（get_group_list）。
func (s *ClientService) GetGroupList(ctx context.Context, noCache bool) ([]domain.GroupInfo, error) {
	action, err := domain.NewGetGroupList(noCache)
	if err != nil {
		return nil, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if err := decodeJSON(payload, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.GroupInfo, 0, len(rows))
	for _, row := range rows {
		info, err := decodeGroupInfo(row)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// decodeGroupInfo decodes one group info object.
// decodeGroupInfo 解析单个群信息对象。
func decodeGroupInfo(payload json.RawMessage) (domain.GroupInfo, error) {
	var row struct {
		GroupID        int64  `json:"group_id"`
		GroupName      string `json:"group_name"`
		MemberCount    int64  `json:"member_count"`
		MaxMemberCount int64  `json:"max_member_count"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return domain.GroupInfo{}, err
	}
	return domain.GroupInfo{
		GroupID:        domain.GroupID(row.GroupID),
		GroupName:      row.GroupName,
		MemberCount:    row.MemberCount,
		MaxMemberCount: row.MaxMemberCount,
	}, nil
}

// GetGroupMemberInfo implements contracts.InfoReader.
// GetGroupMemberInfo 实现 contracts.InfoReader（get_group_member_info）。
func (s *ClientService) GetGroupMemberInfo(ctx context.Context, groupID domain.GroupID, userID domain.UserID, noCache bool) (domain.GroupMemberInfo, error) {
	var out domain.GroupMemberInfo
	action, err := domain.NewGetGroupMemberInfo(groupID, userID, noCache)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	return decodeGroupMemberInfo(payload)
}

// GetGroupMemberList implements contracts.InfoReader.
// GetGroupMemberList 实现 contracts.InfoReader（get_group_member_list）。
func (s *ClientService) GetGroupMemberList(ctx context.Context, groupID domain.GroupID) ([]domain.GroupMemberInfo, error) {
	action, err := domain.NewGetGroupMemberList(groupID)
	if err != nil {
		return nil, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if err := decodeJSON(payload, &rows); err != nil {
		return nil, err
	}
	out := make([]domain.GroupMemberInfo, 0, len(rows))
	for _, row := range rows {
		member, err := decodeGroupMemberInfo(row)
		if err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, nil
}

// decodeGroupMemberInfo decodes one group member object.
// decodeGroupMemberInfo 解析单个群成员对象。
func decodeGroupMemberInfo(payload json.RawMessage) (domain.GroupMemberInfo, error) {
	var row struct {
		GroupID      int64  `json:"group_id"`
		UserID       int64  `json:"user_id"`
		Nickname     string `json:"nickname"`
		Card         string `json:"card"`
		Role         string `json:"role"`
		JoinTime     int64  `json:"join_time"`
		LastSentTime int64  `json:"last_sent_time"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return domain.GroupMemberInfo{}, err
	}
	return domain.GroupMemberInfo{
		GroupID:      domain.GroupID(row.GroupID),
		UserID:       domain.UserID(row.UserID),
		Nickname:     row.Nickname,
		Card:         row.Card,
		Role:         domain.MemberRole(row.Role),
		JoinTime:     row.JoinTime,
		LastSentTime: row.LastSentTime,
		Extra: decodeExtra(payload, "group_id", "user_id", "nickname", "card",
			"role", "join_time", "last_sent_time"),
	}, nil
}

// GetGroupHonorInfo implements contracts.InfoReader.
// GetGroupHonorInfo 实现 contracts.InfoReader（get_group_honor_info）。
func (s *ClientService) GetGroupHonorInfo(ctx context.Context, groupID domain.GroupID, honorType string) (domain.HonorInfo, error) {
	var out domain.HonorInfo
	action, err := domain.NewGetGroupHonorInfo(groupID, honorType)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		GroupID int64  `json:"group_id"`
		Type    string `json:"type"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.HonorInfo{
		GroupID: domain.GroupID(row.GroupID),
		Type:    row.Type,
		Extra:   decodeExtra(payload, "group_id", "type"),
	}, nil
}

// GetCookies implements contracts.InfoReader.
// GetCookies 实现 contracts.InfoReader（get_cookies）。
func (s *ClientService) GetCookies(ctx context.Context, domainName string) (domain.CookiesResult, error) {
	var out domain.CookiesResult
	action, err := domain.NewGetCookies(domainName)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		Cookies string `json:"cookies"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.CookiesResult{Cookies: row.Cookies}, nil
}

// GetCSRFToken implements contracts.InfoReader.
// GetCSRFToken 实现 contracts.InfoReader（get_csrf_token）。
func (s *ClientService) GetCSRFToken(ctx context.Context) (domain.CSRFTokenResult, error) {
	var out domain.CSRFTokenResult
	action, err := domain.NewGetCSRFToken()
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		Token int64 `json:"token"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.CSRFTokenResult{Token: row.Token}, nil
}

// GetCredentials implements contracts.InfoReader.
// GetCredentials 实现 contracts.InfoReader（get_credentials）。
func (s *ClientService) GetCredentials(ctx context.Context, domainName string) (domain.CredentialsResult, error) {
	var out domain.CredentialsResult
	action, err := domain.NewGetCredentials(domainName)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		Cookies   string `json:"cookies"`
		CSRFToken int64  `json:"csrf_token"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.CredentialsResult{Cookies: row.Cookies, CSRFToken: row.CSRFToken}, nil
}

// GetRecord implements contracts.InfoReader.
// GetRecord 实现 contracts.InfoReader（get_record）。
func (s *ClientService) GetRecord(ctx context.Context, file, outFormat string) (domain.FileResult, error) {
	var out domain.FileResult
	action, err := domain.NewGetRecord(file, outFormat)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	return decodeFileResult(payload)
}

// GetImage implements contracts.InfoReader.
// GetImage 实现 contracts.InfoReader（get_image）。
func (s *ClientService) GetImage(ctx context.Context, file string) (domain.FileResult, error) {
	var out domain.FileResult
	action, err := domain.NewGetImage(file)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	return decodeFileResult(payload)
}

// decodeFileResult decodes a file-shaped response.
// decodeFileResult 解析文件形响应。
func decodeFileResult(payload json.RawMessage) (domain.FileResult, error) {
	var row struct {
		File     string `json:"file"`
		Filename string `json:"filename"`
		URL      string `json:"url"`
		Size     int64  `json:"file_size"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return domain.FileResult{}, err
	}
	return domain.FileResult{File: row.File, Filename: row.Filename, URL: row.URL, Size: row.Size}, nil
}

// CanSendImage implements contracts.InfoReader.
// CanSendImage 实现 contracts.InfoReader（can_send_image）。
func (s *ClientService) CanSendImage(ctx context.Context) (domain.BoolCapability, error) {
	return s.canSend(ctx, domain.ActionCanSendImage)
}

// CanSendRecord implements contracts.InfoReader.
// CanSendRecord 实现 contracts.InfoReader（can_send_record）。
func (s *ClientService) CanSendRecord(ctx context.Context) (domain.BoolCapability, error) {
	return s.canSend(ctx, domain.ActionCanSendRecord)
}

func (s *ClientService) canSend(ctx context.Context, name domain.ActionName) (domain.BoolCapability, error) {
	var out domain.BoolCapability
	action := domain.Action{Name: name, Params: map[string]any{}}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		Yes bool `json:"yes"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.BoolCapability{Supported: row.Yes}, nil
}

// GetRuntimeStatus implements contracts.InfoReader.
// GetRuntimeStatus 实现 contracts.InfoReader（get_status）。
func (s *ClientService) GetRuntimeStatus(ctx context.Context) (domain.RuntimeStatus, error) {
	var out domain.RuntimeStatus
	action, err := domain.NewGetStatus()
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		Online bool `json:"online"`
		Good   bool `json:"good"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.RuntimeStatus{
		Online: row.Online,
		Good:   row.Good,
		Extra:  decodeExtra(payload, "online", "good"),
	}, nil
}

// GetVersionInfo implements contracts.InfoReader.
// GetVersionInfo 实现 contracts.InfoReader（get_version_info）。
func (s *ClientService) GetVersionInfo(ctx context.Context) (domain.VersionInfo, error) {
	var out domain.VersionInfo
	action, err := domain.NewGetVersionInfo()
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		AppName         string `json:"app_name"`
		AppVersion      string `json:"app_version"`
		ProtocolVersion string `json:"protocol_version"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	return domain.VersionInfo{
		AppName:         row.AppName,
		AppVersion:      row.AppVersion,
		ProtocolVersion: row.ProtocolVersion,
		Extra:           decodeExtra(payload, "app_name", "app_version", "protocol_version"),
	}, nil
}
