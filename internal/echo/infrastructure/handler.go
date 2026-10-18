// Package infrastructure adapts echo use cases to the bot handler contract.
// Package infrastructure 将 echo 用例适配为 bot contracts.Handler，演示新增业务上下文的标准路径。
package infrastructure

import (
	"context"
	"strings"

	echoapp "github.com/HappyLadySauceM/QQBot/internal/echo/application"
	"github.com/HappyLadySauceM/QQBot/internal/onebot/domain"
)

// Handler routes message events into the echo service.
// Handler 将消息事件路由进 echo 服务。
type Handler struct {
	service *echoapp.Service
	prefix  string
}

// NewHandler builds the adapter.
// NewHandler 构建适配器。
func NewHandler(service *echoapp.Service, prefix string) *Handler {
	return &Handler{service: service, prefix: strings.TrimSpace(prefix)}
}

// Name implements bot contracts.Handler.
// Name 实现 bot contracts.Handler。
func (h *Handler) Name() string { return "echo" }

// Match implements bot contracts.Handler.
// Match 实现 bot contracts.Handler：仅匹配消息事件中的 ping 与"前缀 + 空格"指令。
func (h *Handler) Match(event domain.Event) bool {
	if !event.IsMessage() {
		return false
	}
	text := strings.TrimSpace(event.Text())
	if text == "ping" {
		return true
	}
	return text == h.prefix || strings.HasPrefix(text, h.prefix+" ")
}

// Handle implements bot contracts.Handler.
// Handle 实现 bot contracts.Handler。
func (h *Handler) Handle(ctx context.Context, event domain.Event) error {
	text := strings.TrimSpace(event.Text())
	if text == "ping" {
		return h.service.HandlePing(ctx, event)
	}
	return h.service.HandleEchoCommand(ctx, event)
}
