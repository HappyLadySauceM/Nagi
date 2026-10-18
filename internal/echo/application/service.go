// Package application implements the echo sample context use cases.
// Package application 实现 echo 示例上下文的业务用例：ping 回 pong 与复读。
package application

import (
	"context"
	"fmt"
	"strings"

	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Service answers simple commands through a contracts.Sender.
// Service 通过 contracts.Sender 应答简单指令。
type Service struct {
	sender obcontracts.Sender
	prefix string
}

// NewService binds the service to a sender.
// NewService 将服务绑定到发送端口。
func NewService(sender obcontracts.Sender, prefix string) (*Service, error) {
	if sender == nil {
		return nil, fmt.Errorf("echo: sender required")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, fmt.Errorf("echo: prefix required")
	}
	return &Service{sender: sender, prefix: prefix}, nil
}

// Prefix returns the configured command prefix.
// Prefix 返回配置的指令前缀。
func (s *Service) Prefix() string { return s.prefix }

// HandlePing replies pong to a bare "ping" message.
// HandlePing 对内容为 ping 的消息回复 pong。
func (s *Service) HandlePing(ctx context.Context, event domain.Event) error {
	if strings.TrimSpace(event.Text()) != "ping" {
		return nil
	}
	return s.reply(ctx, event, domain.Text("pong"))
}

// HandleEchoCommand replies with the text after the configured prefix.
// HandleEchoCommand 复读前缀之后的文本。
func (s *Service) HandleEchoCommand(ctx context.Context, event domain.Event) error {
	text := strings.TrimSpace(event.Text())
	rest, ok := strings.CutPrefix(text, s.prefix)
	if !ok {
		return nil
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return s.reply(ctx, event, domain.Text(fmt.Sprintf("usage: %s <text>", s.prefix)))
	}
	return s.reply(ctx, event, domain.Text(rest))
}

// reply routes the answer to the originating group or private chat.
// reply 将应答发送回来源群聊或私聊。
func (s *Service) reply(ctx context.Context, event domain.Event, segments ...domain.Segment) error {
	if event.InGroup() {
		return s.sender.SendGroupMessage(ctx, event.GroupID, segments...)
	}
	return s.sender.SendPrivateMessage(ctx, event.UserID, segments...)
}
