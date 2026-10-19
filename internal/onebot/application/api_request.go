package application

import (
	"context"

	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// SetFriendAddRequest implements contracts.RequestApprover.
// SetFriendAddRequest 实现 contracts.RequestApprover（set_friend_add_request）。
func (s *ClientService) SetFriendAddRequest(ctx context.Context, flag string, approve bool, remark string) error {
	action, err := domain.NewSetFriendAddRequest(flag, approve, remark)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SetGroupAddRequest implements contracts.RequestApprover.
// SetGroupAddRequest 实现 contracts.RequestApprover（set_group_add_request）。
func (s *ClientService) SetGroupAddRequest(ctx context.Context, flag string, subType domain.GroupRequestSubType, approve bool, reason string) error {
	action, err := domain.NewSetGroupAddRequest(flag, subType, approve, reason)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}
