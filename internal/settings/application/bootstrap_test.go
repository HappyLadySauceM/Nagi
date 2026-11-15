package application

import (
	"context"
	"testing"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// TestFromConfigMapsRoleAndContext verifies the bootstrap model reads the new
// Role/Context sections and defaults the shared history budget to 30.
// TestFromConfigMapsRoleAndContext 验证引导模型读取新的 Role/Context 分区，
// 且共享历史预算默认为 30。
func TestFromConfigMapsRoleAndContext(t *testing.T) {
	cfg, err := config.Load(config.Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	settings := FromConfig(cfg)
	if settings.Context.HistoryLimit != 30 {
		t.Fatalf("history limit = %d, want 30", settings.Context.HistoryLimit)
	}
	cfg.Role.BotName = "Nagi"
	cfg.Context.HistoryLimit = 42
	settings = FromConfig(cfg)
	if settings.Role.BotName != "Nagi" || settings.Context.HistoryLimit != 42 {
		t.Fatalf("role/context = %+v/%+v", settings.Role, settings.Context)
	}
}

// TestEnsureMigratesLegacyIntentFields verifies an old persisted settings row
// still exposes its bot name and history budget after loading.
// TestEnsureMigratesLegacyIntentFields 验证旧持久化配置加载后仍保留机器人名与历史预算。
func TestEnsureMigratesLegacyIntentFields(t *testing.T) {
	repo := &fakeRepo{exists: true, revision: settingsdomain.Revision{
		Version: 1,
		Settings: settingsdomain.Settings{
			Intent: settingsdomain.Intent{
				Enabled: true, Provider: "jev", Endpoint: "https://api.example/v1/systemone",
				Model: "jev", TimeoutSeconds: 3, ContinuationThreshold: 0.8,
				InterjectionThreshold: 0.95, AddressedOtherMax: 0.6,
				BotName: "LegacyBot", HistoryLimit: 42,
			},
			Context: settingsdomain.Context{},
		},
	}}
	service, provider := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := provider.Settings()
	if got.Role.BotName != "LegacyBot" || got.Context.HistoryLimit != 42 {
		t.Fatalf("migrated role/context = %+v/%+v", got.Role, got.Context)
	}
	if got.Intent.BotName != "" || got.Intent.HistoryLimit != 0 {
		t.Fatalf("legacy fields not cleared: %+v", got.Intent)
	}
}
