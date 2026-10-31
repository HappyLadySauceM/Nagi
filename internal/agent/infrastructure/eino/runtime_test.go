// Tests for the Eino runtime event loop with a fake chat model.
// Eino 运行时事件循环的测试，使用假聊天模型。
package eino

import (
	"context"
	"errors"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	pc "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
	einoTools "github.com/HappyLadySauceM/Nagi/internal/plugins/infrastructure/eino"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/HappyLadySauceM/Nagi/internal/agent/contracts"
)

// fakeModel returns canned replies in order and records call counts.
// fakeModel 按顺序返回预置回复并记录调用次数。
type fakeModel struct {
	replies []*schema.Message
	err     error
	block   bool
	calls   int
}

func (f *fakeModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.calls++
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(f.replies) == 0 {
		return &schema.Message{Role: schema.Assistant, Content: ""}, nil
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply, nil
}

func (f *fakeModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not used by these tests")
}

func testRequest() contracts.Request {
	return contracts.Request{
		BotID: 100, ConversationID: "conv-1", Kind: "private", TargetID: 200,
		SenderID: 200, MessageID: 5001, JobID: 11,
		Messages: []contracts.Message{
			{Role: "system", Content: "你是助手"},
			{Role: "user", Content: "[用户200] 你好"},
		},
	}
}

// TestRuntimeReturnsAssistantReply verifies text, model and token usage are
// surfaced from the terminal assistant message.
// TestRuntimeReturnsAssistantReply 验证终态助手消息的正文、模型与 token 用量。
func TestRuntimeReturnsAssistantReply(t *testing.T) {
	fake := &fakeModel{replies: []*schema.Message{{
		Role:    schema.Assistant,
		Content: "你好，有什么可以帮你？",
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
			PromptTokens: 11, CompletionTokens: 3,
		}},
	}}}
	runtime := newRuntimeWithModel(Config{Model: "test-model", Timeout: time.Second}, fake)

	result, err := runtime.Run(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Text != "你好，有什么可以帮你？" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.Model != "test-model" {
		t.Fatalf("model = %q", result.Model)
	}
	if result.InputUsage != 11 || result.OutputUsage != 3 {
		t.Fatalf("usage = %d/%d", result.InputUsage, result.OutputUsage)
	}
	if fake.calls != 1 {
		t.Fatalf("model calls = %d, want 1", fake.calls)
	}
}

// TestRuntimePropagatesModelError verifies a model failure fails the run.
// TestRuntimePropagatesModelError 验证模型失败会导致整次运行失败。
func TestRuntimePropagatesModelError(t *testing.T) {
	fake := &fakeModel{err: errors.New("boom")}
	runtime := newRuntimeWithModel(Config{Model: "test-model", Timeout: time.Second}, fake)

	if _, err := runtime.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("expected the model error to surface")
	}
}

// TestRuntimeRequiresMessages verifies an empty request is rejected before any
// model call.
// TestRuntimeRequiresMessages 验证空请求在调用模型前被拒绝。
func TestRuntimeRequiresMessages(t *testing.T) {
	fake := &fakeModel{}
	runtime := newRuntimeWithModel(Config{Model: "test-model", Timeout: time.Second}, fake)

	if _, err := runtime.Run(context.Background(), contracts.Request{}); err == nil {
		t.Fatal("empty messages must be rejected")
	}
	if fake.calls != 0 {
		t.Fatalf("model must not be called, calls = %d", fake.calls)
	}
}

// TestRuntimeHonorsTimeout verifies the per-run deadline aborts a stuck model.
// TestRuntimeHonorsTimeout 验证单次运行超时会中断卡住的模型调用。
func TestRuntimeHonorsTimeout(t *testing.T) {
	fake := &fakeModel{block: true}
	runtime := newRuntimeWithModel(Config{Model: "test-model", Timeout: time.Second}, fake)
	req := testRequest()
	req.Timeout = 30 * time.Millisecond

	started := time.Now()
	_, err := runtime.Run(context.Background(), req)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout not honored, elapsed = %s", elapsed)
	}
}

// TestModelForReusesDefaultAndCachesBudgets verifies that a per-request output
// budget selects a dedicated model while the default budget reuses the shared
// one, and that repeated budgets are built only once.
// TestModelForReusesDefaultAndCachesBudgets 验证按请求输出预算选择专用模型，
// 默认预算复用共享模型，且相同预算只构建一次。
func TestModelForReusesDefaultAndCachesBudgets(t *testing.T) {
	shared := &fakeModel{}
	dedicated := &fakeModel{}
	builds := 0
	r := &Runtime{
		model: shared,
		builder: func(context.Context, int) (model.BaseModel[*schema.Message], error) {
			builds++
			return dedicated, nil
		},
		cfg:    Config{MaxOutputTokens: 100},
		models: make(map[int]model.BaseModel[*schema.Message]),
	}
	ctx := context.Background()

	for _, budget := range []int{0, 100} {
		got, err := r.modelFor(ctx, budget)
		if err != nil || got != model.BaseModel[*schema.Message](shared) {
			t.Fatalf("budget %d must reuse the default model (err=%v)", budget, err)
		}
	}
	first, err := r.modelFor(ctx, 64)
	if err != nil {
		t.Fatalf("model for 64 tokens: %v", err)
	}
	second, err := r.modelFor(ctx, 64)
	if err != nil {
		t.Fatalf("second model for 64 tokens: %v", err)
	}
	if builds != 1 || first != second || first != model.BaseModel[*schema.Message](dedicated) {
		t.Fatalf("budget cache: builds=%d same=%v", builds, first == second)
	}
}

// TestToSchemaMessagesMapsRoles verifies role mapping and unknown-role fallback.
// TestToSchemaMessagesMapsRoles 验证角色映射与未知角色回退。
func TestToSchemaMessagesMapsRoles(t *testing.T) {
	messages := toSchemaMessages([]contracts.Message{
		{Role: "system", Content: "s"},
		{Role: "assistant", Content: "a"},
		{Role: "user", Content: "u"},
		{Role: "weird", Content: "x"},
	})
	if len(messages) != 4 {
		t.Fatalf("len = %d", len(messages))
	}
	if messages[0].Role != schema.System || messages[1].Role != schema.Assistant || messages[2].Role != schema.User {
		t.Fatalf("roles = %v/%v/%v", messages[0].Role, messages[1].Role, messages[2].Role)
	}
	if messages[3].Role != schema.User {
		t.Fatalf("unknown role must fall back to user, got %v", messages[3].Role)
	}
	if !strings.Contains(messages[2].Content, "u") {
		t.Fatalf("content lost: %+v", messages[2])
	}
}

type toolCapableModel struct{ *fakeModel }

func (m *toolCapableModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func TestSensitiveToolGrantSkipsTraceWithoutMuting(t *testing.T) {
	for _, sensitive := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "credentials"}[sensitive], func(t *testing.T) {
			secret := "fixture-credential-secret"
			calls := 0
			handler := callbacks.NewHandlerBuilder().OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
				calls++
				return ctx
			}).Build()
			cfg := Config{Timeout: time.Second, MaxSteps: 3, Handlers: []callbacks.Handler{handler}, Tools: func(context.Context, contracts.Request) []tool.BaseTool {
				return einoTools.ToBaseTools([]pc.RegisteredTool{{Descriptor: pc.ToolDescriptor{ID: "qq.get_credentials", Description: "credentials", Sensitive: sensitive}, Invoke: func(context.Context, string) (string, error) { return secret, nil }}})
			}}
			chatModel := &toolCapableModel{&fakeModel{replies: []*schema.Message{
				{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "one", Type: "function", Function: schema.FunctionCall{Name: "qq_get_credentials", Arguments: "{}"}}}},
				{Role: schema.Assistant, Content: "finished"},
			}}}
			ctx := observability.WithContentState(context.Background())
			runtime := newRuntimeWithModel(cfg, chatModel)
			result, err := runtime.Run(ctx, testRequest())
			if err != nil || result.Text != "finished" {
				t.Fatalf("run: %+v %v", result, err)
			}
			// A credential-capable run detaches raw callbacks (they cannot read the
			// content state) but must not suppress the transcript or mute replies.
			// 可能接触凭证的运行不挂载原始回调（它们读不到内容状态），但不得抑制
			// 正文或静音回复。
			if observability.ContentSuppressed(ctx) {
				t.Fatal("grant alone suppressed the transcript")
			}
			if sensitive && calls != 0 {
				t.Fatalf("credential run kept raw callbacks: %d", calls)
			}
			if !sensitive && calls == 0 {
				t.Fatal("ordinary run lost callbacks")
			}
		})
	}
}
