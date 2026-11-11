package infrastructure

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/memory/domain"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

type staticModelResolver struct {
	profile models.Profile
	apiKey  string
}

func (r staticModelResolver) ResolveModel(context.Context, string, int64, string, int64) (models.Resolved, error) {
	return models.Resolved{Profile: r.profile, APIKey: r.apiKey}, nil
}

func (r staticModelResolver) BilibiliCredential(context.Context) (string, error) { return "", nil }

// realModelExtractorFromConfig builds the real model extractor from NAGI_CONFIG.
// The caller must gate the test on NAGI_REAL_MODEL and keep credentials out of logs.
func realModelExtractorFromConfig(t *testing.T) *ModelExtractor {
	t.Helper()
	configPath := os.Getenv("NAGI_CONFIG")
	if configPath == "" {
		t.Skip("NAGI_CONFIG is required for the real model probe")
	}
	cfg, err := config.Load(config.Options{File: configPath, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.AI.APIKey == "" || cfg.AI.BaseURL == "" || cfg.AI.Model == "" {
		t.Skip("AI endpoint or credential is not configured")
	}
	return NewModelExtractor(staticModelResolver{
		profile: models.Profile{
			BaseURL:         cfg.AI.BaseURL,
			Model:           cfg.AI.Model,
			MaxOutputTokens: cfg.AI.MaxOutputTokens,
			TimeoutSeconds:  int(cfg.AI.GenerationTimeout.D() / time.Second),
		},
		apiKey: cfg.AI.APIKey,
	})
}

// TestModelExtractorRealModelProbe exercises the real OpenAI-compatible
// extractor with synthetic messages. It is opt-in so ordinary tests make no
// network calls or spend tokens.
// TestModelExtractorRealModelProbe 使用合成消息验证真实 OpenAI 兼容抽取器；
// 该用例显式开启，普通测试不会发起网络调用或消耗额度。
func TestModelExtractorRealModelProbe(t *testing.T) {
	if os.Getenv("NAGI_REAL_MODEL") != "1" {
		t.Skip("set NAGI_REAL_MODEL=1 to run the real model probe")
	}
	configPath := os.Getenv("NAGI_CONFIG")
	if configPath == "" {
		t.Skip("NAGI_CONFIG is required for the real model probe")
	}
	cfg, err := config.Load(config.Options{File: configPath, LookupEnv: func(string) (string, bool) { return "", false }})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.AI.APIKey == "" || cfg.AI.BaseURL == "" || cfg.AI.Model == "" {
		t.Skip("AI endpoint or credential is not configured")
	}
	extractor := NewModelExtractor(staticModelResolver{
		profile: models.Profile{
			BaseURL:         cfg.AI.BaseURL,
			Model:           cfg.AI.Model,
			MaxOutputTokens: cfg.AI.MaxOutputTokens,
			TimeoutSeconds:  int(cfg.AI.GenerationTimeout.D() / time.Second),
		},
		apiKey: cfg.AI.APIKey,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	candidates, err := extractor.Extract(ctx, domain.ExtractionRequest{
		Scope: domain.ExtractionScope{BotID: 1, ConversationID: "real-model-probe", ScopeType: "group", ScopeID: 700},
		Messages: []domain.ExtractionSourceMessage{
			{ID: 1, SenderID: 9001, Text: "用户A喜欢乌龙茶"},
			{ID: 2, SenderID: 9002, Text: "用户B周末喜欢爬山"},
		},
		MaxRunes: 4000,
	})
	if err != nil {
		t.Fatalf("real model extraction: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("real model returned no candidates for synthetic probe messages")
	}
	for index, candidate := range candidates {
		if candidate.SourceMessageID != 1 && candidate.SourceMessageID != 2 {
			t.Fatalf("candidate source outside synthetic input: %+v", candidate)
		}
		t.Logf("synthetic candidate %d: source=%d subject=%s content=%q", index, candidate.SourceMessageID, candidate.Subject, candidate.Content)
	}
	t.Logf("real model returned %d synthetic candidates", len(candidates))
}
