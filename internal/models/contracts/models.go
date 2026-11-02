// Package contracts publishes model configuration snapshots and credential ports.
// Package contracts 发布模型配置快照及凭证端口。
package contracts

import (
	"context"
	"fmt"
	"maps"
	"time"
)

// Profile describes one model endpoint. Secret is encrypted at rest and omitted from public views.
// Profile 描述一个模型端点。Secret 静态加密存储，公开视图会移除它。
type Profile struct {
	HTTPHeaders     map[string]string `json:",omitempty"`
	HeaderSecrets   map[string]string `json:",omitempty"`
	VerifiedVersion int64
	VerifiedAt      time.Time
	ID              string
	Name            string
	Usage           string
	Protocol        string
	BaseURL         string
	Model           string
	Images          bool
	Tools           bool
	Voice           string
	Thinking        bool
	ReasoningEffort string
	ContextWindow   int
	MaxOutputTokens int
	TimeoutSeconds  int
	Version         int64
	HasCredential   bool
	Secret          string `json:",omitempty"`
}

// Catalog is the versioned model catalogue persisted alongside system settings.
// Catalog 是随系统配置持久化的版本化模型目录。
type Catalog struct {
	Initialized           bool
	Profiles              []Profile
	Defaults              map[string]string
	Selections            map[string]string
	BilibiliSecret        string `json:",omitempty"`
	HasBilibiliCredential bool
}

// Resolved freezes a profile and its credential for one run.
// Resolved 固定单次运行所用的模型及凭证。
type Resolved struct {
	Profile Profile
	APIKey  string
}

// Resolver supplies the selected model without exposing credentials to prompts.
// Resolver 提供选定模型，不向提示词暴露凭证。
type Resolver interface {
	ResolveModel(context.Context, string, int64, string, int64) (Resolved, error)
	BilibiliCredential(context.Context) (string, error)
}

// Snapshotter pins every model and credential for a complete message run.
// Snapshotter 为完整消息运行固定所有模型和凭证。
type Snapshotter interface {
	ModelSnapshot(context.Context) context.Context
}

// Cipher encrypts stored credentials using a persistent application key.
// Cipher 使用持久化应用密钥加解密凭证。
type Cipher interface {
	Seal(string) (string, error)
	Open(string) (string, error)
}

// Clone prevents callers from mutating a published snapshot.
// Clone 防止调用者修改已发布快照。
func (c Catalog) Clone() Catalog {
	out := c
	out.Profiles = append([]Profile{}, c.Profiles...)
	for i := range out.Profiles {
		out.Profiles[i].HTTPHeaders = maps.Clone(c.Profiles[i].HTTPHeaders)
		out.Profiles[i].HeaderSecrets = maps.Clone(c.Profiles[i].HeaderSecrets)
	}
	out.Defaults = map[string]string{}
	for k, v := range c.Defaults {
		out.Defaults[k] = v
	}
	out.Selections = map[string]string{}
	for k, v := range c.Selections {
		out.Selections[k] = v
	}
	return out
}

// Public removes ciphertext from management responses.
// Public 从管理响应中移除密文。
func (c Catalog) Public() Catalog {
	out := c.Clone()
	out.HasBilibiliCredential = out.BilibiliSecret != ""
	out.BilibiliSecret = ""
	for i := range out.Profiles {
		out.Profiles[i].HasCredential = out.Profiles[i].Secret != ""
		out.Profiles[i].Secret = ""
		out.Profiles[i].HTTPHeaders = map[string]string{}
		for key := range out.Profiles[i].HeaderSecrets {
			out.Profiles[i].HTTPHeaders[key] = ""
		}
		out.Profiles[i].HeaderSecrets = nil
	}
	return out
}

// Verified binds successful testing to the current configuration version.
// Verified 将测试成功绑定到当前配置版本。
func (p Profile) Verified() bool {
	return p.Version > 0 && p.VerifiedVersion == p.Version && !p.VerifiedAt.IsZero()
}

// Tester validates a draft against the actual upstream service without persisting it.
// Tester 使用真实上游服务验证草稿，不执行持久化。
type Tester interface {
	TestModel(context.Context, Resolved) error
}

// Selected resolves the profile by session, account template, then global default.
// Selected 按会话、账号模板、全局默认解析模型。
func (c Catalog) Selected(usage string, bot int64, kind string, target int64) (Profile, bool) {
	id := c.Defaults[usage]
	if usage == "generation" {
		if ref := c.Selections[fmt.Sprintf("%d:%s:%d", bot, kind, 0)]; ref != "" {
			id = ref
		}
		if ref := c.Selections[fmt.Sprintf("%d:%s:%d", bot, kind, target)]; ref != "" {
			id = ref
		}
	}
	for _, p := range c.Profiles {
		if p.ID == id && p.Usage == usage {
			return p, p.Verified()
		}
	}
	return Profile{}, false
}

// Audio calls native audio protocols using the same resolved model in tests and runs.
// Audio 在测试与运行中使用同一已解析模型调用原生音频协议。
type Audio interface {
	Transcribe(context.Context, Resolved, []byte) (string, error)
	Synthesize(context.Context, Resolved, string) ([]byte, error)
}
