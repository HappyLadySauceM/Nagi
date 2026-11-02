package assembly

import (
	"context"
	"errors"
	"fmt"
	intentdomain "github.com/HappyLadySauceM/Nagi/internal/intent/domain"
	intentinfra "github.com/HappyLadySauceM/Nagi/internal/intent/infrastructure"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	modelinfra "github.com/HappyLadySauceM/Nagi/internal/models/infrastructure"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type modelTester struct{}

func (modelTester) TestModel(ctx context.Context, m models.Resolved) error {
	if m.Profile.Usage != "intent" {
		return (modelinfra.HTTPTester{}).TestModel(ctx, m)
	}
	p := m.Profile
	cfg := intentinfra.JevConfig{HTTPHeaders: p.HTTPHeaders, Endpoint: p.BaseURL, Model: p.Model, APIKey: m.APIKey, Timeout: time.Minute, BotName: "TestBot", Thinking: p.Thinking, ReasoningEffort: p.ReasoningEffort, ContextWindow: p.ContextWindow, MaxOutputTokens: p.MaxOutputTokens}
	input := intentdomain.Input{Task: intentdomain.Task{Kind: "private", Message: intentdomain.Message{Text: "Hello TestBot"}}}
	if p.Protocol == "openai" {
		cfg.Endpoint = strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
		c := intentinfra.LocalClient{Config: cfg, Client: &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		if _, err := c.Decide(ctx, input); err != nil {
			return fmt.Errorf("意图候选评分测试失败，请检查模型、凭证和接口协议")
		}
		return nil
	}
	client, err := intentinfra.NewJevClient(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return fmt.Errorf("SystemOne接口配置无效")
	}
	if _, err = client.Decide(ctx, input); err != nil {
		if errors.Is(err, intentinfra.ErrDecisionModelUnavailable) {
			return intentinfra.ErrDecisionModelUnavailable
		}
		return fmt.Errorf("SystemOne测试失败：请检查判定接口地址、服务端决策模型加载状态和凭证")
	}
	return nil
}
