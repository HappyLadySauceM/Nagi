// Bootstrap derives the initial settings from the YAML configuration.
// Bootstrap 从 YAML 配置推导初始设置。
package application

import (
	"time"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// FromConfig converts the file configuration into the editable model. It is
// used both as the seed of the database row and as the "restore defaults"
// baseline, so the two can never drift apart.
// FromConfig 把文件配置转换为可编辑模型。它同时充当数据库行的初值与
// 「恢复默认」的基线，两者因此不会彼此漂移。
func FromConfig(cfg *config.Config) settingsdomain.Settings {
	settings := settingsdomain.Settings{
		AI: settingsdomain.AI{
			Enabled:                  cfg.AI.Enabled,
			BaseURL:                  cfg.AI.BaseURL,
			Model:                    cfg.AI.Model,
			MaxOutputTokens:          cfg.AI.MaxOutputTokens,
			GenerationTimeoutSeconds: seconds(cfg.AI.GenerationTimeout.D()),
		},
		Intent: settingsdomain.Intent{
			Enabled:               cfg.Intent.Enabled,
			Provider:              cfg.Intent.Provider,
			Endpoint:              cfg.Intent.Endpoint,
			Model:                 cfg.Intent.Model,
			BotName:               cfg.Intent.BotName,
			TimeoutSeconds:        seconds(cfg.Intent.Timeout.D()),
			ContinuationThreshold: cfg.Intent.ContinuationThreshold,
			InterjectionThreshold: cfg.Intent.InterjectionThreshold,
			AddressedOtherMax:     cfg.Intent.AddressedOtherMax,
			HistoryLimit:          cfg.Intent.HistoryLimit,
		},
		Agent: settingsdomain.Agent{
			MaxSteps:              cfg.Agent.MaxSteps,
			MaxToolCalls:          cfg.Agent.MaxToolCalls,
			MaxRunDurationSeconds: seconds(cfg.Agent.MaxRunDuration.D()),
			Sending: settingsdomain.Sending{
				MaxMessagesPerRun:   cfg.Agent.Sending.MaxMessagesPerRun,
				MaxCharsPerMessage:  cfg.Agent.Sending.MaxCharsPerMessage,
				MaxTotalCharsPerRun: cfg.Agent.Sending.MaxTotalCharsPerRun,
			},
		},
		Messages: settingsdomain.Messages{
			RetentionDays: cfg.Messages.RetentionDays,
		},
		Tools: settingsdomain.Tools{
			// Providers are always registered, so the seed only carries limits
			// and permission defaults.
			// 提供方始终注册，因此初值只包含调用限制与权限默认值。
			TimeoutSeconds:     seconds(cfg.Tools.Timeout.D()),
			MaxResultBytes:     cfg.Tools.MaxResultBytes,
			PermissionDefaults: copyBoolMap(cfg.Tools.PermissionDefaults),
		},
	}
	settings.Normalize()
	return settings
}

func seconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int(duration / time.Second)
}

func copyBoolMap(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
