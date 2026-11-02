package infrastructure

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
	"io"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
)

//go:embed testdata/probe.wav
var probeSpeech []byte

//go:embed testdata/probe.png
var probeImage []byte

//go:embed testdata/probe.mp3
var probeMP3 []byte

// HTTPTester probes OpenAI-compatible generation and audio endpoints.
// HTTPTester 验证 OpenAI 兼容生成和音频端点。
type HTTPTester struct{ Client *http.Client }

func (t HTTPTester) call(ctx context.Context, m models.Resolved, path, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.Profile.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("请求地址无效")
	}
	req.Header.Set("Content-Type", contentType)
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	}
	// Never forward credentials to redirects or include upstream response bodies in errors.
	// 不向重定向转发凭证，不在错误中包含上游正文。
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := modelhttp.Client(&copyClient, m.Profile.HTTPHeaders).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("测试已取消或超时")
		}
		return nil, fmt.Errorf("无法连接模型服务")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("模型服务返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(b) > 8<<20 {
		return nil, fmt.Errorf("模型响应不可用或过大")
	}
	return b, nil
}
func (t HTTPTester) chat(ctx context.Context, m models.Resolved, kind string) error {
	content := any("Reply with OK.")
	if kind == "image" {
		content = []any{map[string]any{"type": "text", "text": "Describe the image briefly."}, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(probeImage)}}}
	}
	request := map[string]any{"model": m.Profile.Model, "messages": []any{map[string]any{"role": "user", "content": content}}, "stream": false}
	if m.Profile.Thinking {
		effort := m.Profile.ReasoningEffort
		if effort == "" {
			effort = "medium"
		}
		request["reasoning_effort"] = effort
	} else {
		request["reasoning_effort"] = "none"
	}
	limit := 64
	if m.Profile.Thinking {
		limit = 2048
	}
	if m.Profile.MaxOutputTokens > 0 && m.Profile.MaxOutputTokens < limit {
		limit = m.Profile.MaxOutputTokens
	}
	request["max_tokens"] = limit
	if kind == "tools" {
		request["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "model_probe", "description": "Call this function to confirm tool support", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		request["messages"] = []any{map[string]any{"role": "user", "content": "Call the model_probe tool exactly once with an empty JSON object. Do not answer in text."}}
		if m.Profile.Thinking {
			request["tool_choice"] = "auto"
		} else {
			request["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": "model_probe"}}
		}
	}
	b, _ := json.Marshal(request)
	b, err := t.call(ctx, m, "/chat/completions", "application/json", b)
	if err != nil {
		return err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   string
				ToolCalls []struct {
					Function struct {
						Name      string
						Arguments string
					}
				} `json:"tool_calls"`
			}
		}
	}
	if json.Unmarshal(b, &out) != nil || len(out.Choices) == 0 {
		return fmt.Errorf("生成接口响应格式无效")
	}
	message := out.Choices[0].Message
	if kind == "tools" {
		if len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "model_probe" || !json.Valid([]byte(message.ToolCalls[0].Function.Arguments)) {
			return fmt.Errorf("工具调用能力测试未通过")
		}
	} else if strings.TrimSpace(message.Content) == "" {
		return fmt.Errorf("模型未返回有效文本")
	}
	return nil
}
func (t HTTPTester) TestModel(ctx context.Context, m models.Resolved) error {
	switch m.Profile.Usage {
	case "generation":
		for _, kind := range []string{"text", "image", "tools"} {
			if kind == "image" && !m.Profile.Images || kind == "tools" && !m.Profile.Tools {
				continue
			}
			if err := t.chat(ctx, m, kind); err != nil {
				return fmt.Errorf("%s测试失败：%w", map[string]string{"text": "文字", "image": "图片", "tools": "工具调用"}[kind], err)
			}
		}
		return nil
	case "asr":
		if m.Profile.Protocol == "ark-plan" {
			_, err := (ArkAudio{Client: t.Client}).Transcribe(ctx, m, probeSpeech)
			return err
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, _ := writer.CreateFormFile("file", "probe.wav")
		_, _ = file.Write(probeSpeech)
		_ = writer.WriteField("model", m.Profile.Model)
		_ = writer.WriteField("response_format", "json")
		_ = writer.Close()
		b, err := t.call(ctx, m, "/audio/transcriptions", writer.FormDataContentType(), body.Bytes())
		if err != nil {
			return err
		}
		var out struct{ Text string }
		if json.Unmarshal(b, &out) != nil || strings.TrimSpace(out.Text) == "" {
			return fmt.Errorf("语音识别未返回有效文本")
		}
		return nil
	case "tts":
		b, _ := json.Marshal(map[string]string{"model": m.Profile.Model, "voice": m.Profile.Voice, "input": "Hello, this is a voice test.", "response_format": "mp3"})
		var err error
		if m.Profile.Protocol == "ark-plan" {
			b, err = (ArkAudio{Client: t.Client}).Synthesize(ctx, m, "你好，这是语音测试。")
		} else {
			b, err = t.call(ctx, m, "/audio/speech", "application/json", b)
		}
		if err != nil {
			return err
		}
		if len(b) < 4 || !(string(b[:3]) == "ID3" || b[0] == 0xff && b[1]&0xe0 == 0xe0) {
			return fmt.Errorf("语音合成未返回有效 MP3 音频")
		}
		cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-f", "mp3", "-i", "pipe:0", "-show_entries", "stream=codec_type", "-of", "csv=p=0")
		cmd.Stdin = bytes.NewReader(b)
		out, err := cmd.Output()
		if err != nil || !strings.Contains(string(out), "audio") {
			return fmt.Errorf("语音合成音频无法解码")
		}
		return nil
	}
	return fmt.Errorf("不支持的模型测试用途")
}
