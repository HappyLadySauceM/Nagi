package application

import (
	"context"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// SetGroupKick implements contracts.GroupAdmin.
// SetGroupKick 实现 contracts.GroupAdmin（set_group_kick）。
func (s *ClientService) SetGroupKick(ctx context.Context, groupID domain.GroupID, userID domain.UserID, rejectAddRequest bool) error {
	action, err := domain.NewSetGroupKick(groupID, userID, rejectAddRequest)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupBan implements contracts.GroupAdmin.
// SetGroupBan 实现 contracts.GroupAdmin（set_group_ban）。
func (s *ClientService) SetGroupBan(ctx context.Context, groupID domain.GroupID, userID domain.UserID, durationSeconds int64) error {
	action, err := domain.NewSetGroupBan(groupID, userID, durationSeconds)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupAnonymousBan implements contracts.GroupAdmin.
// SetGroupAnonymousBan 实现 contracts.GroupAdmin（set_group_anonymous_ban）。
func (s *ClientService) SetGroupAnonymousBan(ctx context.Context, groupID domain.GroupID, flag string, durationSeconds int64) error {
	action, err := domain.NewSetGroupAnonymousBan(groupID, flag, durationSeconds)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupWholeBan implements contracts.GroupAdmin.
// SetGroupWholeBan 实现 contracts.GroupAdmin（set_group_whole_ban）。
func (s *ClientService) SetGroupWholeBan(ctx context.Context, groupID domain.GroupID, enable bool) error {
	action, err := domain.NewSetGroupWholeBan(groupID, enable)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupAdmin implements contracts.GroupAdmin.
// SetGroupAdmin 实现 contracts.GroupAdmin（set_group_admin）。
func (s *ClientService) SetGroupAdmin(ctx context.Context, groupID domain.GroupID, userID domain.UserID, enable bool) error {
	action, err := domain.NewSetGroupAdmin(groupID, userID, enable)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupAnonymous implements contracts.GroupAdmin.
// SetGroupAnonymous 实现 contracts.GroupAdmin（set_group_anonymous）。
func (s *ClientService) SetGroupAnonymous(ctx context.Context, groupID domain.GroupID, enable bool) error {
	action, err := domain.NewSetGroupAnonymous(groupID, enable)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupCard implements contracts.GroupAdmin.
// SetGroupCard 实现 contracts.GroupAdmin（set_group_card）。
func (s *ClientService) SetGroupCard(ctx context.Context, groupID domain.GroupID, userID domain.UserID, card string) error {
	action, err := domain.NewSetGroupCard(groupID, userID, card)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupName implements contracts.GroupAdmin.
// SetGroupName 实现 contracts.GroupAdmin（set_group_name）。
func (s *ClientService) SetGroupName(ctx context.Context, groupID domain.GroupID, name string) error {
	action, err := domain.NewSetGroupName(groupID, name)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupLeave implements contracts.GroupAdmin.
// SetGroupLeave 实现 contracts.GroupAdmin（set_group_leave）。
func (s *ClientService) SetGroupLeave(ctx context.Context, groupID domain.GroupID, dismiss bool) error {
	action, err := domain.NewSetGroupLeave(groupID, dismiss)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupSpecialTitle implements contracts.GroupAdmin.
// SetGroupSpecialTitle 实现 contracts.GroupAdmin（set_group_special_title）。
func (s *ClientService) SetGroupSpecialTitle(ctx context.Context, groupID domain.GroupID, userID domain.UserID, title string) error {
	action, err := domain.NewSetGroupSpecialTitle(groupID, userID, title)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}
