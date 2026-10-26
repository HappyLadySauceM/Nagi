// Eino ADK runtime: one ChatModelAgent per run, driven by the ReplyExecutor.
// Eino ADK 运行时：每次回复构建一个 ChatModelAgent，由 ReplyExecutor 驱动。
package eino

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	einoschema "github.com/cloudwego/eino/schema"

	"github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
)

// defaultMaxSteps bounds the ReAct loop when the caller does not specify one.
// defaultMaxSteps 在调用方未指定时限制 ReAct 循环轮数。
const defaultMaxSteps = 6

// agentName labels this agent in Eino spans and logs.
// agentName 在 Eino span 与日志中标识该 Agent。
const agentName = "nagi_replier"

// Config configures the runtime. BaseURL points at any OpenAI-compatible
// endpoint; Timeout bounds one HTTP call and MaxOutputTokens one reply.
// Config 配置运行时。BaseURL 指向任意 OpenAI 兼容端点；
// Timeout 限制单次 HTTP 调用，MaxOutputTokens 限制单条回复。
type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	MaxOutputTokens int
	// Timeout bounds one whole run; HTTPTimeout bounds a single model call.
	// Timeout 限制整次运行；HTTPTimeout 限制单次模型调用。
	Timeout     time.Duration
	HTTPTimeout time.Duration
	// MaxSteps is the default ReAct bound used when a request does not set one.
	// MaxSteps 是请求未指定时使用的默认 ReAct 轮数上限。
	MaxSteps int
	// Tools resolves the immutable tool snapshot of one run; nil disables tools.
	// Tools 解析单次运行的不可变工具快照；nil 表示不启用工具。
	Tools func(ctx context.Context, req contracts.Request) []tool.BaseTool
	// Handlers are optional observability callbacks; the Langfuse handler is
	// registered per run so it never becomes a second global export pipeline.
	// Handlers 是可选的观测回调；Langfuse handler 按 Run 注册，
	// 避免形成第二条全局导出管线。
	Handlers []callbacks.Handler
}

// maxCachedModels bounds the per-token-budget model cache. A request may set
// its own output budget, and building a model is cheap but not free.
// maxCachedModels 限制按输出预算缓存的模型数量。请求可以指定自己的输出预算，
// 构建模型代价不高但也不为零。
const maxCachedModels = 4

// Runtime implements contracts.Runtime on top of Eino ADK.
// Runtime 基于 Eino ADK 实现 contracts.Runtime。
type Runtime struct {
	// model is the default chat model built from Config.MaxOutputTokens.
	// model 是按 Config.MaxOutputTokens 构建的默认聊天模型。
	model model.BaseModel[*einoschema.Message]
	// builder creates a model for another output budget; nil when a test
	// injected the model directly, in which case the default model is reused.
	// builder 按其他输出预算创建模型；测试直接注入模型时为 nil，
	// 此时始终复用默认模型。
	builder func(ctx context.Context, maxTokens int) (model.BaseModel[*einoschema.Message], error)
	cfg     Config

	mu     sync.RWMutex
	models map[int]model.BaseModel[*einoschema.Message]
}

// newRuntimeWithModel wires a runtime around an existing model; it exists so
// tests can exercise the event loop without an HTTP endpoint.
// newRuntimeWithModel 用已有模型装配运行时；用于测试在不访问 HTTP 端点的情况下
// 验证事件循环。
func newRuntimeWithModel(cfg Config, chatModel model.BaseModel[*einoschema.Message]) *Runtime {
	return &Runtime{model: chatModel, cfg: cfg}
}

// NewRuntime validates the configuration and builds the chat model once; the
// model is stateless per request and safe to reuse.
// NewRuntime 校验配置并只构建一次聊天模型；模型按请求无状态，可安全复用。
func NewRuntime(ctx context.Context, cfg Config) (*Runtime, error) {
	if cfg.BaseURL == "" || cfg.Model == "" {
		return nil, fmt.Errorf("agent: eino base-url and model are required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 25 * time.Second
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = defaultMaxSteps
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = 400
	}
	builder := func(ctx context.Context, maxTokens int) (model.BaseModel[*einoschema.Message], error) {
		return openai.NewChatModel(ctx, &openai.ChatModelConfig{
			BaseURL:   cfg.BaseURL,
			APIKey:    cfg.APIKey,
			Model:     cfg.Model,
			Timeout:   cfg.HTTPTimeout,
			MaxTokens: &maxTokens,
		})
	}
	chatModel, err := builder(ctx, cfg.MaxOutputTokens)
	if err != nil {
		return nil, fmt.Errorf("agent: build eino chat model: %w", err)
	}
	return &Runtime{
		model:   chatModel,
		builder: builder,
		cfg:     cfg,
		models:  make(map[int]model.BaseModel[*einoschema.Message]),
	}, nil
}

// modelFor resolves the chat model of one run. A zero or default budget reuses
// the shared model; other budgets are built once and cached.
// modelFor 解析单次运行的聊天模型。零值或默认预算复用共享模型；
// 其他预算只构建一次并缓存。
func (r *Runtime) modelFor(ctx context.Context, maxTokens int) (model.BaseModel[*einoschema.Message], error) {
	if maxTokens <= 0 || maxTokens == r.cfg.MaxOutputTokens || r.builder == nil {
		return r.model, nil
	}
	r.mu.RLock()
	cached, ok := r.models[maxTokens]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}
	built, err := r.builder(ctx, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("agent: build eino chat model for %d tokens: %w", maxTokens, err)
	}
	r.mu.Lock()
	if len(r.models) < maxCachedModels {
		r.models[maxTokens] = built
	}
	r.mu.Unlock()
	return built, nil
}

// Run executes one reply. The event iterator is always drained to its terminal
// state: stopping early would leave Eino's model/tool spans incomplete.
// Run 执行一次回复。事件迭代器必须消费到终态：提前停止会让 Eino 的
// 模型/工具 span 不完整。
func (r *Runtime) Run(ctx context.Context, req contracts.Request) (contracts.Result, error) {
	if len(req.Messages) == 0 {
		return contracts.Result{}, fmt.Errorf("agent: eino run requires messages")
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = r.cfg.Timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	chatModel, err := r.modelFor(runCtx, req.MaxOutputTokens)
	if err != nil {
		return contracts.Result{}, err
	}
	steps := req.MaxSteps
	if steps <= 0 {
		steps = r.cfg.MaxSteps
	}
	toolsConfig := adk.ToolsConfig{}
	exposed := 0
	if r.cfg.Tools != nil {
		if snapshot := r.cfg.Tools(runCtx, req); len(snapshot) > 0 {
			toolsConfig.ToolsNodeConfig.Tools = snapshot
			exposed = len(snapshot)
		}
	}
	agent, err := adk.NewChatModelAgent(runCtx, &adk.ChatModelAgentConfig{
		Name:          agentName,
		Model:         chatModel,
		ToolsConfig:   toolsConfig,
		MaxIterations: steps,
	})
	if err != nil {
		return contracts.Result{}, fmt.Errorf("agent: build eino agent: %w", err)
	}
	runner := adk.NewRunner(runCtx, adk.RunnerConfig{Agent: agent})

	opts := make([]adk.AgentRunOption, 0, 1)
	if len(r.cfg.Handlers) > 0 {
		opts = append(opts, adk.WithCallbacks(r.cfg.Handlers...))
	}
	iterator := runner.Run(runCtx, toSchemaMessages(req.Messages), opts...)

	result := contracts.Result{Model: r.cfg.Model, ToolsExposed: exposed}
	answered := false
	for {
		event, ok := iterator.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			return contracts.Result{}, fmt.Errorf("agent: eino run: %w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, outErr := materialize(event.Output.MessageOutput)
		if outErr != nil {
			return contracts.Result{}, outErr
		}
		if message == nil || message.Role != einoschema.Assistant {
			continue
		}
		answered = true
		// Tool-calling turns carry no text; keep the last real answer instead of
		// blanking it.
		// 工具调用轮次没有正文；保留最后一段真实回复而不是清空。
		if message.Content != "" {
			result.Text = message.Content
		}
		if usage := message.ResponseMeta; usage != nil && usage.Usage != nil {
			result.InputUsage += int64(usage.Usage.PromptTokens)
			result.OutputUsage += int64(usage.Usage.CompletionTokens)
		}
	}
	if !answered {
		return contracts.Result{}, fmt.Errorf("agent: eino run produced no assistant message")
	}
	return result, nil
}

// materialize returns the complete message for one event, draining the stream
// when streaming is enabled.
// materialize 返回事件对应的完整消息；开启流式时把分片读完。
func materialize(variant *adk.TypedMessageVariant[*einoschema.Message]) (*einoschema.Message, error) {
	if variant.IsStreaming {
		if variant.MessageStream == nil {
			return nil, nil
		}
		return drainStream(variant.MessageStream)
	}
	return variant.Message, nil
}

// drainStream concatenates one message stream into a single message.
// drainStream 把一个消息流拼成单条消息。
func drainStream(stream *einoschema.StreamReader[*einoschema.Message]) (*einoschema.Message, error) {
	defer stream.Close()
	var merged *einoschema.Message
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("agent: read eino stream: %w", err)
		}
		if chunk == nil {
			continue
		}
		if merged == nil {
			copied := *chunk
			merged = &copied
			continue
		}
		merged.Content += chunk.Content
		merged.ToolCalls = append(merged.ToolCalls, chunk.ToolCalls...)
		if chunk.ResponseMeta != nil {
			merged.ResponseMeta = chunk.ResponseMeta
		}
	}
	return merged, nil
}

// toSchemaMessages converts business messages into Eino schema messages.
// toSchemaMessages 把业务消息转换为 Eino 的 schema 消息。
func toSchemaMessages(messages []contracts.Message) []*einoschema.Message {
	out := make([]*einoschema.Message, 0, len(messages))
	for _, message := range messages {
		role := einoschema.RoleType(message.Role)
		switch role {
		case einoschema.System, einoschema.User, einoschema.Assistant, einoschema.Tool:
		default:
			role = einoschema.User
		}
		out = append(out, &einoschema.Message{Role: role, Content: message.Content})
	}
	return out
}
