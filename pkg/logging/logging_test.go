package logging

import (
	"log/slog"
	"testing"
)

func TestNewAcceptsLevelNamesCaseInsensitively(t *testing.T) {
	// 验证级别名大小写不敏感并生成 logger。
	logger, err := New("INFO")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	if logger == nil {
		t.Fatal("logger is nil")
	}
}

func TestNewRejectsInvalidLevel(t *testing.T) {
	// 验证非法级别返回错误。
	if _, err := New("verbose"); err == nil {
		t.Fatal("expected error for invalid level")
	}
}

func TestParseLevelReturnsSlogLevel(t *testing.T) {
	// 验证 ParseLevel 输出标准库级别。
	lv, err := ParseLevel("warn")
	if err != nil {
		t.Fatalf("parse level: %v", err)
	}
	if lv != slog.LevelWarn {
		t.Fatalf("level = %v", lv)
	}
}
