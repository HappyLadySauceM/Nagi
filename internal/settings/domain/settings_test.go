// Tests for the settings validation model.
// 配置校验模型的测试。
package domain

import (
	"errors"
	"testing"
)

func validSettings() Settings {
	return Settings{
		AI: AI{
			Enabled: true, BaseURL: "https://llm.example/v1", Model: "test-model",
			MaxOutputTokens: 400, GenerationTimeoutSeconds: 25,
		},
		Intent: Intent{
			Enabled: true, Provider: "jev", Endpoint: "https://api.typesafe.ai/v1/systemone",
			Model: "jev-latest", TimeoutSeconds: 3,
			ContinuationThreshold: 0.8, InterjectionThreshold: 0.95, AddressedOtherMax: 0.6,
			HistoryLimit: 30,
		},
		Agent: Agent{
			MaxSteps: 6, MaxToolCalls: 5, MaxRunDurationSeconds: 30,
			Sending: Sending{MaxMessagesPerRun: 0, MaxCharsPerMessage: 4500, MaxTotalCharsPerRun: 0},
		},
		Messages: Messages{RetentionDays: 30},
		Tools: Tools{
			TimeoutSeconds: 5, MaxResultBytes: 8192,
			PermissionDefaults: map[string]bool{"qq.reply.current": true},
		},
	}
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	return invalid.Fields
}

func TestValidateAcceptsCompleteSettings(t *testing.T) {
	// 验证完整配置通过校验。
	settings := validSettings()
	if err := settings.Validate(nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidateRejectsAIDisabledPipeline(t *testing.T) {
	// 只开生成不开流水线会让消息只入库不回复，必须拒绝。
	settings := validSettings()
	settings.Intent.Enabled = false
	fields := fieldsOf(t, settings.Validate(nil))
	if fields["AI.Enabled"] == "" {
		t.Fatalf("fields = %+v, want AI.Enabled", fields)
	}
}

func TestValidateRejectsBadURLAndRanges(t *testing.T) {
	// 验证地址与数值边界。
	settings := validSettings()
	settings.AI.BaseURL = "ftp://example.com"
	settings.Agent.MaxSteps = 0
	settings.Tools.MaxResultBytes = 8
	fields := fieldsOf(t, settings.Validate(nil))
	for _, key := range []string{"AI.BaseURL", "Agent.MaxSteps", "Tools.MaxResultBytes"} {
		if fields[key] == "" {
			t.Fatalf("missing %s in %+v", key, fields)
		}
	}
}

func TestValidateRejectsThresholdOutOfRange(t *testing.T) {
	// 阈值必须落在 (0,1] 区间。
	settings := validSettings()
	settings.Intent.InterjectionThreshold = 1.2
	fields := fieldsOf(t, settings.Validate(nil))
	if fields["Intent.InterjectionThreshold"] == "" {
		t.Fatalf("fields = %+v", fields)
	}
}

func TestValidateAllowsStalePermissionKeys(t *testing.T) {
	// 权限默认值是内部基线：提供方下线留下的陈旧键不得阻塞保存。
	settings := validSettings()
	settings.Tools.PermissionDefaults["qq.unknown"] = true
	if err := settings.Validate(map[string]bool{"qq.reply.current": true}); err != nil {
		t.Fatalf("stale permission key must not fail validation: %v", err)
	}
}

func TestValidateRejectsBadRetention(t *testing.T) {
	// 留存天数必须在 0-3650 之间，0 表示不清理。
	settings := validSettings()
	settings.Messages.RetentionDays = -1
	fields := fieldsOf(t, settings.Validate(nil))
	if fields["Messages.RetentionDays"] == "" {
		t.Fatalf("fields = %+v", fields)
	}
}

func TestValidateAllowsUnlimitedSending(t *testing.T) {
	// 0 表示不限：整轮为 0 时不再比较单条与整轮。
	settings := validSettings()
	settings.Agent.Sending = Sending{MaxMessagesPerRun: 0, MaxCharsPerMessage: 4500, MaxTotalCharsPerRun: 0}
	if err := settings.Validate(nil); err != nil {
		t.Fatalf("unlimited sending must be valid: %v", err)
	}
}

func TestNormalizeTrimsAndFillsMap(t *testing.T) {
	// 规范化去空格并补全权限映射，使语义相同的配置逐字段可比。
	settings := validSettings()
	settings.AI.Model = "  model-x  "
	settings.Tools.PermissionDefaults = nil
	settings.Normalize()
	if settings.AI.Model != "model-x" || settings.Tools.PermissionDefaults == nil {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestCloneIsDeep(t *testing.T) {
	// 深拷贝后修改副本不得影响原值。
	settings := validSettings()
	clone := settings.Clone()
	clone.Tools.PermissionDefaults["qq.reply.current"] = false
	if !settings.Tools.PermissionDefaults["qq.reply.current"] {
		t.Fatal("clone mutated the source settings")
	}
}
