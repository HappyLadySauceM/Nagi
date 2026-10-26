package application

import (
	"context"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// SetRestart implements contracts.Maintenance.
// SetRestart 实现 contracts.Maintenance（set_restart）。
func (s *ClientService) SetRestart(ctx context.Context, delayMillis int64) error {
	action, err := domain.NewSetRestart(delayMillis)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// CleanCache implements contracts.Maintenance.
// CleanCache 实现 contracts.Maintenance（clean_cache）。
func (s *ClientService) CleanCache(ctx context.Context) error {
	action, err := domain.NewCleanCache()
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}
