package application

import (
	"testing"

	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

// TestDefaultIntentThresholdsMatchParticipationBaseline guards the product
// default thresholds against silent drift from the fixed participation
// baseline. A deliberate threshold change must be confirmed by product and
// update both the default config snapshots and the baseline report.
// TestDefaultIntentThresholdsMatchParticipationBaseline 防止产品默认阈值相对固定参与判断基线
// 静默漂移。有意调整必须经产品确认，并同时更新默认配置快照与基线报告。
func TestDefaultIntentThresholdsMatchParticipationBaseline(t *testing.T) {
	cfg, err := config.Load(config.Options{LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	got := cfg.Intent
	if got.ContinuationThreshold != baselineThresholds.Continuation ||
		got.InterjectionThreshold != baselineThresholds.Interjection ||
		got.AddressedOtherMax != baselineThresholds.AddressedOtherMax {
		t.Fatalf("默认意图阈值发生漂移：continuation=%.2f interjection=%.2f addressed_other_max=%.2f；参与判断基线固定为 %.2f/%.2f/%.2f。默认策略变更必须由产品确认；若确认调整，请同时更新 pkg/config 快照、参与基线报告和相关回归。",
			got.ContinuationThreshold, got.InterjectionThreshold, got.AddressedOtherMax,
			baselineThresholds.Continuation, baselineThresholds.Interjection, baselineThresholds.AddressedOtherMax)
	}
}
