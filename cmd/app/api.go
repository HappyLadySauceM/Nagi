package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// APIServer wraps the management HTTP server with graceful shutdown.
// APIServer 包装管理 HTTP 服务器并提供优雅关闭。
type APIServer struct {
	srv    *http.Server
	logger *slog.Logger
}

// NewAPIServer builds the server with conservative timeouts.
// NewAPIServer 以保守超时构建服务器。
func NewAPIServer(addr string, handler http.Handler, logger *slog.Logger) *APIServer {
	return &APIServer{
		srv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		logger: logger,
	}
}

// Run listens and serves until ctx is canceled, then shuts down gracefully.
// Run 监听并服务，ctx 取消后优雅关闭。
func (a *APIServer) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen management api %s: %w", a.srv.Addr, err)
	}
	a.logger.Info("management api listening", "addr", a.srv.Addr)

	serveErr := make(chan error, 1)
	go func() { serveErr <- a.srv.Serve(ln) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.srv.Shutdown(shutdownCtx); err != nil {
			a.logger.Warn("management api graceful shutdown failed", "error", err)
			_ = a.srv.Close()
		}
		select {
		case <-serveErr:
		default:
		}
		return nil
	}
}
