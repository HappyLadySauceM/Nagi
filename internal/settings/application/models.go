package application

import (
	"context"
	"fmt"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
	"net/url"
	"strings"
	"time"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/google/uuid"
)

// ModelEdit keeps credential changes separate from the public model snapshot.
// ModelEdit 将凭证变更与公开模型快照分开。
type ModelEdit struct {
	Profile         models.Profile
	Credential      *string
	ClearCredential bool
	Version         int64
}
type ModelsView struct {
	Catalog models.Catalog
	Version int64
}

func publicSettings(s settingsdomain.Settings) settingsdomain.Settings {
	out := s.Clone()
	applyModelConnections(&out)
	out.Models = out.Models.Public()
	return out
}

// Models returns the safe catalogue and its optimistic-lock revision.
// Models 返回安全目录及其乐观锁版本。
func (s *Service) Models() ModelsView {
	v := s.provider.Current()
	return ModelsView{Catalog: v.Settings.Models.Public(), Version: v.Version}
}

func (s *Service) storeModels(ctx context.Context, c models.Catalog, expected int64, actor string) error {
	current := s.provider.Current()
	if current.Version != expected {
		return ErrConflict
	}
	next := current.Settings.Clone()
	next.Models = c.Clone()
	applyModelConnections(&next)
	if s.repo == nil {
		s.provider.Store(Snapshot{Settings: next, Version: expected + 1, UpdatedAt: time.Now(), UpdatedBy: actor})
		return nil
	}
	rev, ok, err := s.repo.Update(ctx, next, expected, actor)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	s.provider.Store(snapshotOf(rev))
	return nil
}

// EnsureModels imports file credentials once; later restarts never resurrect deleted secrets.
// EnsureModels 仅导入一次文件凭证，后续重启不会复活已删除密钥。
func (s *Service) EnsureModels(ctx context.Context) error {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	cur := s.provider.Current()
	if cur.Settings.Models.Initialized || s.cipher == nil {
		return nil
	}
	c := models.Catalog{Initialized: true, Defaults: map[string]string{}, Selections: map[string]string{}, Profiles: []models.Profile{}}
	live := cur.Settings
	seeds := []models.Profile{{ID: uuid.NewString(), Name: "原生成模型", Usage: "generation", Protocol: "openai", BaseURL: live.AI.BaseURL, Model: live.AI.Model, Images: true, Tools: true, MaxOutputTokens: live.AI.MaxOutputTokens, TimeoutSeconds: live.AI.GenerationTimeoutSeconds, Version: 1}, {ID: uuid.NewString(), Name: "原意图模型", Usage: "intent", Protocol: live.Intent.Provider, BaseURL: live.Intent.Endpoint, Model: live.Intent.Model, TimeoutSeconds: live.Intent.TimeoutSeconds, Version: 1}}
	for i, p := range seeds {
		if p.Model == "" || p.BaseURL == "" {
			continue
		}
		if p.Usage == "intent" && p.Protocol == "openai" {
			p.BaseURL = strings.TrimSuffix(strings.TrimRight(p.BaseURL, "/"), "/chat/completions")
		}
		key := s.legacyAIKey
		if i == 1 {
			key = s.legacyIntentKey
		}
		encrypted, err := s.cipher.Seal(key)
		if err != nil {
			return err
		}
		p.Secret = encrypted
		c.Profiles = append(c.Profiles, p)
		c.Defaults[p.Usage] = p.ID
	}
	return s.storeModels(ctx, c, cur.Version, "model-import")
}

// SaveModel creates or replaces one endpoint without accepting client-supplied ciphertext.
// SaveModel 创建或替换一个端点，不接受客户端提供的密文。
func (s *Service) SaveModel(ctx context.Context, id string, edit ModelEdit, actor string) (ModelsView, error) {
	s.modelMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.modelMu.Unlock()
		}
	}()
	if s.cipher == nil {
		return ModelsView{}, fmt.Errorf("model credentials unavailable")
	}
	c := s.provider.Settings().Models.Clone()
	p := edit.Profile
	headers, err := modelhttp.Normalize(p.HTTPHeaders)
	if err != nil {
		return ModelsView{}, err
	}
	p.HTTPHeaders = nil
	p.HeaderSecrets = map[string]string{}
	var oldHeaders map[string]string
	p.TimeoutSeconds = 60
	if p.ContextWindow < 0 || p.ContextWindow > 10000000 {
		return ModelsView{}, fmt.Errorf("invalid context window")
	}
	if len(p.ReasoningEffort) > 64 || strings.ContainsAny(p.ReasoningEffort, "\r\n\t ") {
		return ModelsView{}, fmt.Errorf("思考级别须为不含空白的字符串，最多64个字符")
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Model = strings.TrimSpace(p.Model)
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && !(p.Protocol == "ark-plan" && p.Usage == "asr" && (u.Scheme == "wss" || u.Scheme == "ws"))) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || p.Name == "" || p.Model == "" {
		return ModelsView{}, fmt.Errorf("invalid model name, endpoint or model")
	}
	if p.Usage != "generation" && p.Usage != "intent" && p.Usage != "asr" && p.Usage != "tts" {
		return ModelsView{}, fmt.Errorf("invalid model usage")
	}
	if p.Protocol == "" {
		p.Protocol = "openai"
		if p.Usage == "intent" {
			p.Protocol = "jev"
		}
	}
	if p.Protocol != "openai" && (p.Protocol != "jev" || p.Usage != "intent") && (p.Protocol != "ark-plan" || (p.Usage != "asr" && p.Usage != "tts")) {
		return ModelsView{}, fmt.Errorf("invalid model protocol")
	}

	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 || (p.Usage == "intent" && p.TimeoutSeconds > 120) {
		return ModelsView{}, fmt.Errorf("model timeout must be 1-300 seconds")
	}
	if p.Usage == "generation" && (p.MaxOutputTokens < 0 || p.MaxOutputTokens > 1000000) {
		return ModelsView{}, fmt.Errorf("invalid output token budget")
	}
	if p.Usage == "tts" && strings.TrimSpace(p.Voice) == "" {
		return ModelsView{}, fmt.Errorf("voice is required")
	}
	index := -1
	for i, old := range c.Profiles {
		if old.ID == id {
			index = i
			if old.Usage != p.Usage {
				for _, ref := range c.Defaults {
					if ref == id {
						return ModelsView{}, fmt.Errorf("被引用的模型不能修改用途")
					}
				}
				for _, ref := range c.Selections {
					if ref == id {
						return ModelsView{}, fmt.Errorf("被引用的模型不能修改用途")
					}
				}
			}
			p.Secret = old.Secret
			oldHeaders = old.HeaderSecrets
			p.Version = old.Version + 1
		}
	}
	if id != "" && index < 0 {
		return ModelsView{}, fmt.Errorf("model not found")
	}
	if id == "" {
		id = uuid.NewString()
		p.Version = 1
		p.Secret = ""
	}
	for name, value := range headers {
		if value == "" {
			if encrypted, ok := oldHeaders[name]; ok {
				p.HeaderSecrets[name] = encrypted
				continue
			}
			return ModelsView{}, fmt.Errorf("新增 HTTP 请求头必须填写值")
		}
		encrypted, sealErr := s.cipher.Seal(value)
		if sealErr != nil {
			return ModelsView{}, fmt.Errorf("无法保存请求头")
		}
		p.HeaderSecrets[name] = encrypted
	}
	p.ID = id
	p.HasCredential = false
	if edit.ClearCredential {
		p.Secret = ""
	}
	if edit.Credential != nil {
		p.Secret, err = s.cipher.Seal(*edit.Credential)
		if err != nil {
			return ModelsView{}, err
		}
	}
	if s.provider.Current().Version != edit.Version {
		return ModelsView{}, ErrConflict
	}
	key, err := s.cipher.Open(p.Secret)
	if err != nil {
		return ModelsView{}, fmt.Errorf("无法读取模型凭证")
	}
	if s.modelTester == nil {
		return ModelsView{}, fmt.Errorf("模型测试服务不可用")
	}
	testProfile := p
	testProfile.HTTPHeaders, err = s.openHeaders(p.HeaderSecrets)
	testProfile.HeaderSecrets = nil
	if err != nil {
		return ModelsView{}, err
	}
	tester := s.modelTester
	s.modelMu.Unlock()
	locked = false
	testCtx, cancel := context.WithTimeout(ctx, time.Minute)
	testErr := tester.TestModel(testCtx, models.Resolved{Profile: testProfile, APIKey: key})
	cancel()
	if testErr != nil {
		return ModelsView{}, fmt.Errorf("模型测试失败：%s", testErr.Error())
	}
	if err = ctx.Err(); err != nil {
		return ModelsView{}, err
	}
	s.modelMu.Lock()
	locked = true
	if s.provider.Current().Version != edit.Version {
		return ModelsView{}, ErrConflict
	}
	p.VerifiedVersion = p.Version
	p.VerifiedAt = time.Now().UTC()
	if _, ready := c.Selected("generation", 0, "", 0); p.Usage == "generation" && !ready {
		c.Defaults["generation"] = p.ID
	}
	if index < 0 {
		c.Profiles = append(c.Profiles, p)
	} else {
		c.Profiles[index] = p
	}
	if err = s.storeModels(ctx, c, edit.Version, actor); err != nil {
		return ModelsView{}, err
	}
	return s.Models(), nil
}

func (s *Service) DeleteModel(ctx context.Context, id string, version int64, actor string) (ModelsView, error) {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	c := s.provider.Settings().Models.Clone()
	for _, ref := range c.Defaults {
		if ref == id {
			return ModelsView{}, fmt.Errorf("无法删除：该模型仍被设为全局默认模型。请先在对应的默认模型下拉框中选择“未配置”或其他模型，再重试删除。")
		}
	}
	for _, ref := range c.Selections {
		if ref == id {
			return ModelsView{}, fmt.Errorf("无法删除：该模型仍被会话或账号默认模板引用。请先在对应策略的生成模型选择中改为继承默认或其他模型，再重试删除。")
		}
	}
	found := false
	out := c.Profiles[:0]
	for _, p := range c.Profiles {
		if p.ID == id {
			found = true
		} else {
			out = append(out, p)
		}
	}
	if !found {
		return ModelsView{}, fmt.Errorf("model not found")
	}
	c.Profiles = out
	if err := s.storeModels(ctx, c, version, actor); err != nil {
		return ModelsView{}, err
	}
	return s.Models(), nil
}

// ConfigureModels updates default selections and the optional site credential.
// ConfigureModels 更新默认选择及可选的站点凭证。
func (s *Service) ConfigureModels(ctx context.Context, defaults map[string]string, cookie *string, clear bool, version int64, actor string) (ModelsView, error) {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	c := s.provider.Settings().Models.Clone()
	for usage, id := range defaults {
		if usage != "generation" && usage != "intent" && usage != "asr" && usage != "tts" {
			return ModelsView{}, fmt.Errorf("invalid usage")
		}
		if id != "" {
			found := false
			for _, p := range c.Profiles {
				if p.ID == id && p.Usage == usage {
					if !p.Verified() {
						return ModelsView{}, fmt.Errorf("%s 模型需要重新测试", usage)
					}
					found = true
				}
			}
			if !found {
				return ModelsView{}, fmt.Errorf("invalid default model")
			}
		}
		c.Defaults[usage] = id
	}
	if clear {
		c.BilibiliSecret = ""
	}
	if cookie != nil {
		if s.cipher == nil {
			return ModelsView{}, fmt.Errorf("credentials unavailable")
		}
		if strings.ContainsAny(*cookie, "\r\n") {
			return ModelsView{}, fmt.Errorf("invalid cookie")
		}
		var err error
		c.BilibiliSecret, err = s.cipher.Seal(*cookie)
		if err != nil {
			return ModelsView{}, err
		}
	}
	if err := s.storeModels(ctx, c, version, actor); err != nil {
		return ModelsView{}, err
	}
	return s.Models(), nil
}

func modelScopeKey(bot int64, kind string, target int64) string {
	return fmt.Sprintf("%d:%s:%d", bot, kind, target)
}
func (s *Service) SelectModel(ctx context.Context, bot int64, kind string, target int64, id string, version int64, actor string) error {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	if bot <= 0 || target < 0 || (kind != "private" && kind != "group") {
		return fmt.Errorf("invalid model scope")
	}
	c := s.provider.Settings().Models.Clone()
	if id != "" {
		found := false
		for _, p := range c.Profiles {
			if p.ID == id && p.Usage == "generation" && p.Verified() {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("invalid generation model")
		}
	}
	key := modelScopeKey(bot, kind, target)
	if id == "" {
		delete(c.Selections, key)
	} else {
		c.Selections[key] = id
	}
	return s.storeModels(ctx, c, version, actor)
}

type modelSnapshotKey struct{}

// ModelSnapshot pins the catalogue across intent, generation and media calls.
// ModelSnapshot 固定意图、生成及媒体调用共享的模型目录。
func (s *Service) ModelSnapshot(ctx context.Context) context.Context {
	if _, ok := ctx.Value(modelSnapshotKey{}).(models.Catalog); ok {
		return ctx
	}
	return context.WithValue(ctx, modelSnapshotKey{}, s.provider.Settings().Models.Clone())
}
func (s *Service) modelCatalog(ctx context.Context) models.Catalog {
	if c, ok := ctx.Value(modelSnapshotKey{}).(models.Catalog); ok {
		return c
	}
	return s.provider.Settings().Models
}

func (s *Service) ResolveModel(ctx context.Context, usage string, bot int64, kind string, target int64) (models.Resolved, error) {
	if err := ctx.Err(); err != nil {
		return models.Resolved{}, err
	}
	c := s.modelCatalog(ctx)
	id := c.Defaults[usage]
	if usage == "generation" {
		if ref := c.Selections[modelScopeKey(bot, kind, 0)]; ref != "" {
			id = ref
		}
		if ref := c.Selections[modelScopeKey(bot, kind, target)]; ref != "" {
			id = ref
		}
	}
	for _, p := range c.Profiles {
		if p.ID == id && p.Usage == usage {
			if !p.Verified() {
				return models.Resolved{}, fmt.Errorf("%s 模型需要重新测试", usage)
			}
			if s.cipher == nil {
				return models.Resolved{}, fmt.Errorf("credentials unavailable")
			}
			p.TimeoutSeconds = 60
			key, err := s.cipher.Open(p.Secret)
			p.Secret = ""
			if err != nil {
				return models.Resolved{}, fmt.Errorf("无法读取模型凭证")
			}
			p.HTTPHeaders, err = s.openHeaders(p.HeaderSecrets)
			p.HeaderSecrets = nil
			return models.Resolved{Profile: p, APIKey: key}, err
		}
	}
	return models.Resolved{}, fmt.Errorf("%s model is not configured", usage)
}
func (s *Service) BilibiliCredential(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.cipher == nil {
		return "", fmt.Errorf("credentials unavailable")
	}
	return s.cipher.Open(s.modelCatalog(ctx).BilibiliSecret)
}

func applyModelConnections(next *settingsdomain.Settings) {
	next.AI.Enabled = false
	for _, p := range next.Models.Profiles {
		if p.Usage == "generation" && p.Verified() {
			next.AI.Enabled = true
		}
	}
	// Keep runtime connection fields in sync while all credentials live in the catalogue.
	// 同步运行时连接字段，所有凭证只存放在模型目录中。
	for _, p := range next.Models.Profiles {
		switch p.ID {
		case next.Models.Defaults["generation"]:
			next.AI.BaseURL = p.BaseURL
			next.AI.Model = p.Model
			next.AI.MaxOutputTokens = p.MaxOutputTokens
			if next.AI.MaxOutputTokens == 0 {
				next.AI.MaxOutputTokens = 400
			}
			if next.AI.MaxOutputTokens > 32000 {
				next.AI.MaxOutputTokens = 32000
			}
			next.AI.GenerationTimeoutSeconds = p.TimeoutSeconds
		case next.Models.Defaults["intent"]:
			next.Intent.Provider = p.Protocol
			next.Intent.Endpoint = p.BaseURL
			next.Intent.Model = p.Model
			next.Intent.TimeoutSeconds = p.TimeoutSeconds
		}
	}
}

func (s *Service) openHeaders(secrets map[string]string) (map[string]string, error) {
	headers := map[string]string{}
	for name, encrypted := range secrets {
		value, err := s.cipher.Open(encrypted)
		if err != nil {
			return nil, fmt.Errorf("无法读取模型请求头")
		}
		headers[name] = value
	}
	return headers, nil
}
