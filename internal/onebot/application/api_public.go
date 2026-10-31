package application

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// ExecutePublic preserves the standard response shape for a validated command.
// ExecutePublic 为经过校验的命令保留标准响应形状。
func (s *ClientService) ExecutePublic(ctx context.Context, c domain.PublicCommand) (domain.PublicResult, error) {
	a, err := c.Action()
	if err != nil {
		return domain.PublicResult{}, err
	}
	response, err := s.call(ctx, a)
	if err != nil {
		return domain.PublicResult{}, err
	}
	out := domain.PublicResult{Async: response.async}
	if len(response.payload) > 0 {
		if err := json.Unmarshal(response.payload, &out.Data); err != nil {
			return out, fmt.Errorf("onebot: invalid response data: %w", err)
		}
		var receipt struct {
			MessageID int64 `json:"message_id"`
		}
		if json.Unmarshal(response.payload, &receipt) == nil {
			out.MessageID = receipt.MessageID
		}
	}
	return out, nil
}
