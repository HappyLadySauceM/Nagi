// Package assembly is the composition root binding ports to adapters.
// Package assembly 是组合根：将端口与具体适配器显式绑定，不含业务规则。
package assembly

import (
	"fmt"
	"log/slog"
	"time"

	botapp "github.com/HappyLadySauceM/QQBot/internal/bot/application"
	botcontracts "github.com/HappyLadySauceM/QQBot/internal/bot/contracts"
	echoapp "github.com/HappyLadySauceM/QQBot/internal/echo/application"
	echoinfra "github.com/HappyLadySauceM/QQBot/internal/echo/infrastructure"
	obapp "github.com/HappyLadySauceM/QQBot/internal/onebot/application"
	obcontracts "github.com/HappyLadySauceM/QQBot/internal/onebot/contracts"
	obinfra "github.com/HappyLadySauceM/QQBot/internal/onebot/infrastructure"
	"github.com/HappyLadySauceM/QQBot/pkg/config"
)

// handshakeTimeout bounds each dial attempt.
// handshakeTimeout 限定单次拨号时限。
const handshakeTimeout = 10 * time.Second

// Runtime holds the assembled runtime components.
// Runtime 持有已装配的运行时组件。
type Runtime struct {
	Transport  *obinfra.Transport
	Onebot     *obapp.ClientService
	Dispatcher *botapp.Dispatcher
	Handlers   []botcontracts.Handler
}

// Assemble wires the transport, client service, dispatcher and business handlers.
// Assemble 依次装配传输、客户端服务、分发器与业务处理器。
func Assemble(cfg *config.Config, logger *slog.Logger) (*Runtime, error) {
	transport := obinfra.NewTransport(obinfra.Options{
		URL:              cfg.Onebot.URL,
		AccessToken:      cfg.Onebot.AccessToken,
		HandshakeTimeout: handshakeTimeout,
		ReconnectInitial: cfg.Onebot.Reconnect.Initial.D(),
		ReconnectMax:     cfg.Onebot.Reconnect.Max.D(),
	}, logger)
	client := obapp.NewClientService(transport, cfg.Onebot.CallTimeout.D(), logger)
	transport.Attach(client, client)

	dispatcher := botapp.NewDispatcher(client, logger)
	handlers, err := registerBuiltins(cfg, client, dispatcher)
	if err != nil {
		return nil, err
	}
	return &Runtime{Transport: transport, Onebot: client, Dispatcher: dispatcher, Handlers: handlers}, nil
}

// registerBuiltins mounts built-in sample contexts; new contexts follow this path.
// registerBuiltins 挂载内置示例上下文；新增业务上下文遵循相同路径。
func registerBuiltins(cfg *config.Config, sender obcontracts.Sender, dispatcher *botapp.Dispatcher) ([]botcontracts.Handler, error) {
	var handlers []botcontracts.Handler
	if cfg.Echo.Enabled {
		service, err := echoapp.NewService(sender, cfg.Echo.Prefix)
		if err != nil {
			return nil, fmt.Errorf("assemble echo context: %w", err)
		}
		handlers = append(handlers, echoinfra.NewHandler(service, cfg.Echo.Prefix))
	}
	for _, h := range handlers {
		if err := dispatcher.Register(h); err != nil {
			return nil, fmt.Errorf("register handler %s: %w", h.Name(), err)
		}
	}
	return handlers, nil
}
