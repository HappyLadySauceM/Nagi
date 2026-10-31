package application

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
)

// SendMessage implements contracts.Messenger.
// SendMessage 实现 contracts.Messenger（send_msg）。
func (s *ClientService) SendMessage(ctx context.Context, mt domain.MessageType, target int64, segments ...domain.Segment) error {
	action, err := domain.NewSendMessage(mt, target, segments)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// SendPrivateMessageReceipt sends a private message and returns the platform
// message id reported by the implementation; the id stays 0 when the response
// carries none. Errors keep the contracts.ErrActionTimeout sentinel chain so
// callers can classify unknown-outcome sends.
// SendPrivateMessageReceipt 发送私聊消息并返回实现方回报的 platform message id；
// 响应缺失该字段时返回 0。错误保留 contracts.ErrActionTimeout 哨兵链，
// 供调用方识别结果未知的发送。
func (s *ClientService) SendPrivateMessageReceipt(ctx context.Context, to domain.UserID, segments ...domain.Segment) (int64, error) {
	action, err := domain.NewSendPrivateMessage(to, segments)
	if err != nil {
		return 0, err
	}
	response, err := s.call(ctx, action)
	if err != nil {
		return 0, err
	}
	if response.async {
		return 0, nil
	}
	return decodeMessageID(response.payload)
}

// SendGroupMessageReceipt sends a group message and returns the platform
// message id; see SendPrivateMessageReceipt.
// SendGroupMessageReceipt 发送群聊消息并返回 platform message id；参见 SendPrivateMessageReceipt。
func (s *ClientService) SendGroupMessageReceipt(ctx context.Context, to domain.GroupID, segments ...domain.Segment) (int64, error) {
	action, err := domain.NewSendGroupMessage(to, segments)
	if err != nil {
		return 0, err
	}
	response, err := s.call(ctx, action)
	if err != nil {
		return 0, err
	}
	if response.async {
		return 0, nil
	}
	return decodeMessageID(response.payload)
}

// decodeMessageID extracts data.message_id from a send response payload.
// decodeMessageID 从发送响应载荷中提取 data.message_id。
func decodeMessageID(payload json.RawMessage) (int64, error) {
	var row struct {
		MessageID int64 `json:"message_id"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return 0, fmt.Errorf("onebot: decode message receipt: %w", err)
	}
	return row.MessageID, nil
}

// SendLike implements contracts.Messenger.
// SendLike 实现 contracts.Messenger（send_like）。
func (s *ClientService) SendLike(ctx context.Context, to domain.UserID, times int) error {
	action, err := domain.NewSendLike(to, times)
	if err != nil {
		return err
	}
	return s.do(ctx, action)
}

// GetMessage implements contracts.Messenger.
// GetMessage 实现 contracts.Messenger（get_msg）。
func (s *ClientService) GetMessage(ctx context.Context, id domain.MessageID) (domain.MessageResult, error) {
	var out domain.MessageResult
	action, err := domain.NewGetMessage(id)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	var row struct {
		MessageType string          `json:"message_type"`
		RealID      int64           `json:"real_id"`
		Sender      map[string]any  `json:"sender"`
		Time        int64           `json:"time"`
		MessageID   int64           `json:"message_id"`
		UserID      int64           `json:"user_id"`
		GroupID     int64           `json:"group_id"`
		Message     json.RawMessage `json:"message"`
		RawMessage  string          `json:"raw_message"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return out, err
	}
	segments, err := decodeMessagePayload(row.Message)
	if err != nil {
		return out, err
	}
	if row.UserID == 0 {
		if id, ok := row.Sender["user_id"].(float64); ok {
			row.UserID = int64(id)
		}
	}
	out = domain.MessageResult{
		MessageType: domain.MessageType(row.MessageType), RealID: row.RealID, Sender: row.Sender,
		Time:       row.Time,
		MessageID:  domain.MessageID(row.MessageID),
		UserID:     domain.UserID(row.UserID),
		GroupID:    domain.GroupID(row.GroupID),
		Segments:   segments,
		RawMessage: row.RawMessage,
		Extra:      decodeExtra(payload, "time", "message_id", "user_id", "group_id", "message", "raw_message", "message_type", "real_id", "sender"),
	}
	return out, nil
}

// GetForwardMessages implements contracts.Messenger.
// GetForwardMessages 实现 contracts.Messenger（get_forward_msg）。
func (s *ClientService) GetForwardMessages(ctx context.Context, id string) ([]domain.ForwardNode, error) {
	action, err := domain.NewGetForwardMessage(id)
	if err != nil {
		return nil, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return nil, err
	}
	var row struct {
		Message []struct {
			Data struct {
				UserID   int64           `json:"user_id"`
				Nickname string          `json:"nickname"`
				Time     int64           `json:"time"`
				Content  json.RawMessage `json:"content"`
			} `json:"data"`
		} `json:"message"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return nil, err
	}
	nodes := make([]domain.ForwardNode, 0, len(row.Message))
	for _, node := range row.Message {
		m := node.Data
		segments, err := decodeMessagePayload(m.Content)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, domain.ForwardNode{UserID: domain.UserID(m.UserID), Nickname: m.Nickname, Time: m.Time, Segments: segments})
	}
	return nodes, nil
}

// SendPrivateForward implements contracts.Messenger.
// SendPrivateForward 实现 contracts.Messenger（send_private_forward_msg）。
func (s *ClientService) SendPrivateForward(ctx context.Context, to domain.UserID, nodes ...domain.Segment) (domain.ForwardReceipt, error) {
	var out domain.ForwardReceipt
	action, err := domain.NewSendPrivateForward(to, nodes)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	return decodeForwardReceipt(payload)
}

// SendGroupForward implements contracts.Messenger.
// SendGroupForward 实现 contracts.Messenger（send_group_forward_msg）。
func (s *ClientService) SendGroupForward(ctx context.Context, to domain.GroupID, nodes ...domain.Segment) (domain.ForwardReceipt, error) {
	var out domain.ForwardReceipt
	action, err := domain.NewSendGroupForward(to, nodes)
	if err != nil {
		return out, err
	}
	payload, err := s.Call(ctx, action)
	if err != nil {
		return out, err
	}
	return decodeForwardReceipt(payload)
}

// decodeForwardReceipt decodes the async-safe forward receipt.
// decodeForwardReceipt 解析合并转发回执（retcode=1 时得到零值回执）。
func decodeForwardReceipt(payload json.RawMessage) (domain.ForwardReceipt, error) {
	var row struct {
		MessageID int64  `json:"message_id"`
		ResID     string `json:"res_id"`
	}
	if err := decodeJSON(payload, &row); err != nil {
		return domain.ForwardReceipt{}, fmt.Errorf("onebot: decode forward receipt: %w", err)
	}
	return domain.ForwardReceipt{
		MessageID: domain.MessageID(row.MessageID),
		ResID:     row.ResID,
	}, nil
}
