// Langfuse Prompt Management client used only by explicit local sync.
// 仅在显式本地同步时使用的 Langfuse Prompt Management 客户端。
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

// LangfusePromptConfig configures the optional prompt source. Empty values
// leave the client unconfigured; they never block local prompt handling.
// LangfusePromptConfig 配置可选 Prompt 来源。空值表示未配置，绝不阻塞本地 Prompt。
type LangfusePromptConfig struct {
	BaseURL   string
	PublicKey string
	SecretKey string
	Timeout   time.Duration
}

// LangfusePromptClient fetches one prompt version from the Langfuse public API.
// LangfusePromptClient 从 Langfuse 公共 API 获取一个 Prompt 版本。
type LangfusePromptClient struct {
	cfg    LangfusePromptConfig
	client *http.Client
	logger *slog.Logger
}

// NewLangfusePromptClient builds the client. It returns a usable value even
// when configuration is incomplete so status reporting can say why sync is off.
// NewLangfusePromptClient 构建客户端。即使配置不完整也返回可用对象，
// 以便状态接口说明同步关闭的原因。
func NewLangfusePromptClient(cfg LangfusePromptConfig, logger *slog.Logger) *LangfusePromptClient {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &LangfusePromptClient{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}, logger: logger}
}

// Configured reports whether all connection fields are present.
// Configured 报告连接字段是否齐全。
func (c *LangfusePromptClient) Configured() bool {
	return c != nil && strings.TrimSpace(c.cfg.BaseURL) != "" &&
		strings.TrimSpace(c.cfg.PublicKey) != "" && strings.TrimSpace(c.cfg.SecretKey) != ""
}

// Fetch loads one prompt version. version wins over label; label defaults to
// production when neither is supplied by the caller.
// Fetch 获取一个 Prompt 版本。version 优先于 label；两者都未提供时 label 默认 production。
func (c *LangfusePromptClient) Fetch(ctx context.Context, name string, version int, label string) (domain.RemotePrompt, error) {
	if !c.Configured() {
		return domain.RemotePrompt{}, domain.ErrPromptRemoteNotConfigured
	}
	if strings.TrimSpace(name) == "" {
		return domain.RemotePrompt{}, domain.ErrPromptRemoteInvalid
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/public/v2/prompts/" + url.PathEscape(name)
	query := url.Values{}
	if version > 0 {
		query.Set("version", strconv.Itoa(version))
	} else {
		if label == "" {
			label = "production"
		}
		query.Set("label", label)
	}
	endpoint += "?" + query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.RemotePrompt{}, fmt.Errorf("%w: build request: %v", domain.ErrPromptRemoteUnreachable, err)
	}
	req.SetBasicAuth(c.cfg.PublicKey, c.cfg.SecretKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return domain.RemotePrompt{}, domain.ErrPromptRemoteTimeout
		default:
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return domain.RemotePrompt{}, domain.ErrPromptRemoteTimeout
			}
		}
		c.logger.Warn("langfuse prompt request failed", "error", err)
		return domain.RemotePrompt{}, domain.ErrPromptRemoteUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return domain.RemotePrompt{}, domain.ErrPromptRemoteNotFound
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.RemotePrompt{}, domain.ErrPromptRemoteAuth
	}
	if resp.StatusCode != http.StatusOK {
		c.logger.Warn("langfuse prompt request returned unexpected status", "status", resp.StatusCode)
		return domain.RemotePrompt{}, domain.ErrPromptRemoteUnreachable
	}

	var payload struct {
		Name          string          `json:"name"`
		Version       int             `json:"version"`
		Type          string          `json:"type"`
		Prompt        json.RawMessage `json:"prompt"`
		Config        map[string]any  `json:"config"`
		UpdatedAt     string          `json:"updatedAt"`
		CommitMessage string          `json:"commitMessage"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return domain.RemotePrompt{}, domain.ErrPromptRemoteInvalid
	}
	if strings.TrimSpace(payload.Name) == "" || payload.Version <= 0 || len(payload.Prompt) == 0 || string(payload.Prompt) == "null" {
		return domain.RemotePrompt{}, domain.ErrPromptRemoteInvalid
	}
	return domain.RemotePrompt{
		Name: payload.Name, Version: payload.Version, Type: payload.Type,
		Prompt: payload.Prompt, Config: payload.Config, UpdatedAt: payload.UpdatedAt,
	}, nil
}
