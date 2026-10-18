// Package logging provides structured JSON logging built on slog.
// Package logging 提供基于 slog 的结构化 JSON 日志。
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ParseLevel 解析日志级别，接受 debug/info/warn/error（大小写不敏感）。
func ParseLevel(level string) (slog.Level, error) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(level)))); err != nil {
		return 0, fmt.Errorf("logging: 无效日志级别 %q: %w", level, err)
	}
	return lv, nil
}

// New 按级别创建输出到 stdout 的 JSON slog.Logger。
func New(level string) (*slog.Logger, error) {
	lv, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})), nil
}
