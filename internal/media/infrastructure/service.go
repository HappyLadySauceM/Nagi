// Package infrastructure retrieves and processes bounded media resources.
// Package infrastructure 获取并处理有界媒体资源。
package infrastructure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/internal/observability"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
)

const maxImageBytes = 10 << 20
const maxAudioBytes = 25 << 20

var recordCQ = regexp.MustCompile(`\[CQ:record(?:,[^\]]*)?\]`)

type Reader interface {
	GetImage(context.Context, string) (ob.FileResult, error)
	GetRecord(context.Context, string, string) (ob.FileResult, error)
}
type Accounts interface{ MediaReader(int64) (Reader, bool) }
type Service struct {
	Audio    models.Audio
	Models   models.Resolver
	Accounts Accounts
	HTTP     *http.Client
	CacheDir string
}

// NewService confines media downloads to public network addresses, including redirects.
// NewService 将媒体下载限制在公网地址范围内，重定向同样受限。
func NewService(models models.Resolver, accounts Accounts, cache string) *Service {
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid media host")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("media host unavailable")
		}
		for _, ip := range ips {
			if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() {
				return nil, fmt.Errorf("private media address denied")
			}
		}
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
		}
		return nil, fmt.Errorf("media connection failed")
	}}
	return &Service{Models: models, Accounts: accounts, CacheDir: cache, HTTP: &http.Client{Transport: transport, Timeout: 120 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("invalid redirect")
		}
		req.Header.Del("Cookie")
		return nil
	}}}
}
func (s *Service) fetch(ctx context.Context, raw string, limit int64, headers map[string]string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, fmt.Errorf("invalid media URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid media request")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("media download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if resp.StatusCode == 412 || resp.StatusCode == 429 {
			return nil, mediaFailure("media_rate_limited", "视频平台暂时限制访问，请稍后重试或检查网络与登录凭证")
		}
		return nil, mediaFailure("media_unavailable", fmt.Sprintf("媒体资源不可用（HTTP %d）", resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("media download interrupted")
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("media exceeds size limit")
	}
	return b, nil
}

func (s *Service) Parts(ctx context.Context, bot int64, conversation string, segments []ob.Segment, text string) ([]media.Part, error) {
	plain := ob.PlainText(segments)
	prefix, suffix := text, ""
	if plain != "" {
		if index := strings.Index(text, plain); index >= 0 {
			prefix = text[:index]
			suffix = text[index+len(plain):]
		}
	}
	out := []media.Part{{Text: prefix}}
	images := 0
	for _, seg := range segments {
		switch seg.Type {
		case ob.SegText:
			out = append(out, media.Part{Text: seg.Data["text"]})
		case ob.SegImage:
			images++
			if images > 8 {
				out = append(out, media.Part{Text: "[图片数量超过上限]"})
				continue
			}
			raw := seg.Data["url"]
			if raw == "" && s.Accounts != nil {
				if reader, ok := s.Accounts.MediaReader(bot); ok {
					info, err := reader.GetImage(ctx, seg.Data["file"])
					if err == nil {
						raw = info.URL
						if raw == "" {
							raw = info.File
						}
					}
				}
			}
			var b []byte
			var err error
			inline := strings.TrimPrefix(seg.Data["file"], "base64://")
			if strings.HasPrefix(seg.Data["file"], "base64://") {
				if len(inline) > maxImageBytes*4/3+4 {
					err = fmt.Errorf("image too large")
				} else {
					b, err = base64.StdEncoding.DecodeString(inline)
				}
			} else {
				b, err = s.fetch(ctx, raw, maxImageBytes, nil)
			}
			if err != nil {
				out = append(out, media.Part{Text: "[图片读取失败]"})
				continue
			}
			mime := http.DetectContentType(b)
			if mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" && mime != "image/webp" {
				out = append(out, media.Part{Text: "[不支持的图片格式]"})
				continue
			}
			out = append(out, media.Part{MIME: mime, Base64: base64.StdEncoding.EncodeToString(b)})
		case ob.SegVideo:
			out = append(out, media.Part{Text: "[视频附件，请调用 media_video_inspect，source 使用 attachment:current]"})
		}
	}
	if suffix != "" {
		out = append(out, media.Part{Text: suffix})
	}
	return out, nil
}

func (s *Service) Transcribe(ctx context.Context, bot int64, segments []ob.Segment, text string) (string, error) {
	for _, seg := range segments {
		if seg.Type != ob.SegRecord || seg.Data[media.TranscriptMarker] == "true" {
			continue
		}
		if s.Models == nil {
			return text, fmt.Errorf("ASR model is not configured")
		}
		if _, err := s.Models.ResolveModel(ctx, "asr", 0, "", 0); err != nil {
			return text, err
		}
		if s.Accounts == nil {
			return text, fmt.Errorf("voice account unavailable")
		}
		reader, ok := s.Accounts.MediaReader(bot)
		if !ok {
			return text, fmt.Errorf("voice account unavailable")
		}
		info, err := reader.GetRecord(ctx, seg.Data["file"], "mp3")
		if err != nil {
			return text, fmt.Errorf("voice conversion failed")
		}
		var audio []byte
		if info.Base64 != "" {
			if len(info.Base64) > maxAudioBytes*4/3+4 {
				return text, fmt.Errorf("voice too large")
			}
			audio, err = base64.StdEncoding.DecodeString(info.Base64)
		} else {
			raw := info.URL
			if raw == "" {
				raw = info.File
			}
			if !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "http://") {
				return text, fmt.Errorf("converted voice bytes are unavailable from the bridge")
			}
			audio, err = s.fetch(ctx, raw, maxAudioBytes, nil)
		}
		if err != nil {
			return text, fmt.Errorf("voice unavailable")
		}
		transcript, err := s.transcribeAudio(ctx, audio)
		if err != nil {
			return text, err
		}
		text = strings.TrimSpace(recordCQ.ReplaceAllString(text, ""))
		text += "\n[语音转写] " + transcript
	}
	return text, nil
}
func (s *Service) transcribeAudio(ctx context.Context, audio []byte) (transcript string, err error) {
	if s.Models == nil {
		return "", fmt.Errorf("ASR model is not configured")
	}
	resolved, err := s.Models.ResolveModel(ctx, "asr", 0, "", 0)
	if err != nil {
		return "", err
	}
	auditEvent, auditErr := auditcontracts.StartEvent(ctx, "media.voice.transcribe", map[string]any{
		"model": resolved.Profile.Model, "protocol": resolved.Profile.Protocol, "audio_bytes": len(audio),
	})
	if auditErr != nil {
		return "", auditErr
	}
	defer func() {
		output := map[string]any{"audio_bytes": len(audio)}
		if observability.ContentEnabled() && !observability.ContentSuppressed(ctx) {
			output["transcript"] = transcript
		} else {
			output["content_redacted"] = "observability_export_disabled"
		}
		status, code := "succeeded", ""
		if err != nil {
			status, code = "failed", "asr_failed"
			output["error"] = err.Error()
		}
		if finishErr := auditcontracts.FinishEvent(ctx, auditEvent, status, code, output); finishErr != nil && err == nil {
			err = finishErr
		}
	}()

	if resolved.Profile.Protocol == "ark-plan" {
		if s.Audio == nil {
			return "", fmt.Errorf("原生语音服务不可用")
		}
		return s.Audio.Transcribe(ctx, resolved, audio)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	file, err := w.CreateFormFile("file", "audio.mp3")
	if err != nil {
		return "", err
	}
	if _, err = file.Write(audio); err != nil {
		return "", err
	}
	_ = w.WriteField("model", resolved.Profile.Model)
	_ = w.WriteField("response_format", "json")
	if err = w.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(resolved.Profile.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", resolved.Profile.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", fmt.Errorf("invalid ASR endpoint")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if resolved.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+resolved.APIKey)
	}
	resp, err := modelhttp.Client(http.DefaultClient, resolved.Profile.HTTPHeaders).Do(req)
	if err != nil {
		return "", fmt.Errorf("ASR request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ASR unavailable (%d)", resp.StatusCode)
	}
	var result struct {
		Text string `json:"text"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil || strings.TrimSpace(result.Text) == "" {
		return "", fmt.Errorf("ASR returned no transcript")
	}
	return result.Text, nil
}

func (s *Service) Synthesize(ctx context.Context, text string) (audio []byte, err error) {
	if s.Models == nil {
		return nil, fmt.Errorf("TTS model is not configured")
	}
	resolved, err := s.Models.ResolveModel(ctx, "tts", 0, "", 0)
	if err != nil {
		return nil, err
	}
	auditInput := map[string]any{"model": resolved.Profile.Model, "protocol": resolved.Profile.Protocol}
	if observability.ContentEnabled() && !observability.ContentSuppressed(ctx) {
		auditInput["text"] = text
	} else {
		auditInput["content_redacted"] = "observability_export_disabled"
	}
	auditEvent, auditErr := auditcontracts.StartEvent(ctx, "media.voice.synthesize", auditInput)
	if auditErr != nil {
		return nil, auditErr
	}
	defer func() {
		status, code := "succeeded", ""
		output := map[string]any{"audio_bytes": len(audio)}
		if err != nil {
			status, code = "failed", "tts_failed"
			output["error"] = err.Error()
		}
		if finishErr := auditcontracts.FinishEvent(ctx, auditEvent, status, code, output); finishErr != nil && err == nil {
			err = finishErr
		}
	}()

	if resolved.Profile.Protocol == "ark-plan" {
		if s.Audio == nil {
			return nil, fmt.Errorf("原生语音服务不可用")
		}
		return s.Audio.Synthesize(ctx, resolved, text)
	}
	payload, _ := json.Marshal(map[string]string{"model": resolved.Profile.Model, "voice": resolved.Profile.Voice, "input": text, "response_format": "mp3"})
	ctx, cancel := context.WithTimeout(ctx, time.Duration(resolved.Profile.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", resolved.Profile.BaseURL+"/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid TTS endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	if resolved.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+resolved.APIKey)
	}
	resp, err := modelhttp.Client(http.DefaultClient, resolved.Profile.HTTPHeaders).Do(req)
	if err != nil {
		return nil, fmt.Errorf("TTS request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("TTS unavailable (%d)", resp.StatusCode)
	}
	audio, err = io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes+1))
	if err != nil || len(audio) == 0 || len(audio) > maxAudioBytes {
		return nil, fmt.Errorf("invalid TTS audio")
	}
	return audio, nil
}

// mediaError exposes only a fixed safe message to the model and management traces.
// mediaError 仅向模型及管理追踪暴露安全消息。
type mediaError struct{ code, message string }

func (e *mediaError) Error() string           { return e.message }
func (e *mediaError) UserMessage() string     { return e.message }
func (e *mediaError) ErrorCode() string       { return e.code }
func mediaFailure(code, message string) error { return &mediaError{code: code, message: message} }
