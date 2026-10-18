// Package middleware provides the management API middleware chain.
// Package middleware 提供管理 API 的中间件链。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/HappyLadySauceM/QQBot/cmd/app/types/common"
)

// Middleware decorates an http.Handler.
// Middleware 装饰一个 http.Handler。
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so the first listed runs outermost.
// Chain 按列表顺序应用中间件，第一个位于最外层。
func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type requestIDKey struct{}

// RequestID assigns a random request id and echoes it back.
// RequestID 生成随机请求 ID 并通过响应头回显。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			buf = []byte("00000000")
		}
		id := hex.EncodeToString(buf)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFrom reads the id assigned by the RequestID middleware.
// RequestIDFrom 读取 RequestID 中间件写入的请求 ID。
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// AccessLog logs method, path, status and duration without bodies.
// AccessLog 记录方法、路径、状态与耗时，不记录请求体。
func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFrom(r.Context()))
		})
	}
}

// Recover converts handler panics into safe 500 responses.
// Recover 将处理器 panic 转换为安全的 500 响应。
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("http handler panic",
						"panic", fmt.Sprint(rec),
						"stack", string(debug.Stack()),
						"request_id", RequestIDFrom(r.Context()))
					common.WriteFailure(w, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets conservative response headers.
// SecurityHeaders 设置保守的响应头。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
