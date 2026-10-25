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

// SenderResolver resolves the sender bound to one account; replies must never
// leave through another account.
// SenderResolver 解析账号绑定的发送能力；应答不得从其他账号发出。
type SenderResolver interface {
	Sender(botID int64) (obcontracts.Sender, bool)
}

// Service answers simple commands through the originating account's sender.
// Service 通过来源账号的发送端口应答简单指令。
type Service struct {
	senders SenderResolver
	prefix  string
}

// NewService binds the service to an account-aware sender resolver.
// NewService 将服务绑定到按账号解析的发送端口。
func NewService(senders SenderResolver, prefix string) (*Service, error) {
	if senders == nil {
		return nil, fmt.Errorf("echo: sender resolver required")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, fmt.Errorf("echo: prefix required")
	}
	return &Service{senders: senders, prefix: prefix}, nil
}

// Prefix returns the configured command prefix.
// Prefix 返回配置的指令前缀。
func (s *Service) Prefix() string { return s.prefix }

// HandlePing replies pong to a bare "ping" message from the given account.
// HandlePing 对指定账号收到的内容为 ping 的消息回复 pong。
func (s *Service) HandlePing(ctx context.Context, botID domain.BotID, event domain.Event) error {
	if strings.TrimSpace(event.Text()) != "ping" {
		return nil
	}
	return s.reply(ctx, botID, event, domain.Text("pong"))
}

// HandleEchoCommand replies with the text after the configured prefix.
// HandleEchoCommand 复读前缀之后的文本。
func (s *Service) HandleEchoCommand(ctx context.Context, botID domain.BotID, event domain.Event) error {
	text := strings.TrimSpace(event.Text())
	rest, ok := strings.CutPrefix(text, s.prefix)
	if !ok {
		return nil
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return s.reply(ctx, botID, event, domain.Text(fmt.Sprintf("usage: %s <text>", s.prefix)))
	}
	return s.reply(ctx, botID, event, domain.Text(rest))
}

// reply routes the answer to the originating group or private chat.
// reply 将应答发送回来源群聊或私聊。
func (s *Service) reply(ctx context.Context, botID domain.BotID, event domain.Event, segments ...domain.Segment) error {
	sender, ok := s.senders.Sender(int64(botID))
	if !ok {
		return fmt.Errorf("echo: bot %d is not connected", int64(botID))
	}
	if event.InGroup() {
		return sender.SendGroupMessage(ctx, event.GroupID, segments...)
	}
	return sender.SendPrivateMessage(ctx, event.UserID, segments...)
}
