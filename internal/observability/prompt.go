package observability

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type promptTraceKey struct{}

// PromptTrace contains only version references and hashes.
// PromptTrace 只保存版本引用与哈希。
type PromptTrace struct {
	RenderedHash string        `json:"rendered_hash"`
	TemplateHash string        `json:"template_hash,omitempty"`
	Layers       []PromptLayer `json:"layers,omitempty"`
}

type PromptLayer struct {
	Layer     string `json:"layer"`
	Source    string `json:"source"`
	Version   int    `json:"version,omitempty"`
	VersionID int64  `json:"version_id,omitempty"`
	Hash      string `json:"hash"`
	Remote    string `json:"remote,omitempty"`
	Revision  int    `json:"remote_version,omitempty"`
}

func (p *PromptTrace) AddLayer(layer, source, ref string, version int, versionID int64, hash string) {
	value := PromptLayer{Layer: layer, Source: source, Version: version, VersionID: versionID, Hash: hash}
	if name, ok := strings.CutPrefix(ref, "langfuse-verified:"); source == "langfuse" && ok {
		name, remoteVersion, _ := strings.Cut(name, "@")
		if n, err := strconv.Atoi(remoteVersion); err == nil && n > 0 && name != "" {
			value.Remote, value.Revision = name, n
		}
	}
	p.Layers = append(p.Layers, value)
}

func WithPromptTrace(ctx context.Context, prompt PromptTrace) context.Context {
	return context.WithValue(ctx, promptTraceKey{}, prompt)
}

// Record attaches effective prompt metadata. Langfuse accepts one primary
// prompt per generation. Additional layer versions are kept in metadata.
// Record 附加实际使用的提示词元数据。Langfuse 每次生成只关联一个主提示词，
// 其余层的版本保留在元数据中。
func (p PromptTrace) Record(span trace.Span, generation bool) {
	if !span.IsRecording() || p.RenderedHash == "" {
		return
	}
	layers, _ := json.Marshal(p.Layers)
	span.SetAttributes(
		attribute.String("langfuse.observation.metadata.prompt_rendered_hash", p.RenderedHash),
		attribute.String("langfuse.observation.metadata.prompt_template_hash", p.TemplateHash),
		attribute.String("langfuse.observation.metadata.prompt_layers", string(layers)),
	)
	if generation {
		for _, layer := range p.Layers {
			if layer.Remote != "" {
				span.SetAttributes(
					attribute.String("langfuse.observation.prompt.name", layer.Remote),
					attribute.Int("langfuse.observation.prompt.version", layer.Revision),
				)
				break
			}
		}
	}
}
