package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	plugins "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	pluginscontracts "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type testModels struct{ base string }

func (m testModels) ResolveModel(_ context.Context, usage string, _ int64, _ string, _ int64) (models.Resolved, error) {
	return models.Resolved{Profile: models.Profile{BaseURL: m.base, Model: usage, Voice: "voice-1", TimeoutSeconds: 5}, APIKey: "test-key"}, nil
}
func (testModels) BilibiliCredential(context.Context) (string, error) {
	return "SESSDATA=test-cookie", nil
}

type recordReader struct{}

func (recordReader) GetRecord(context.Context, string, string) (ob.FileResult, error) {
	return ob.FileResult{Base64: base64.StdEncoding.EncodeToString([]byte("audio")), OutFormat: "mp3"}, nil
}
func (recordReader) GetImage(context.Context, string) (ob.FileResult, error) {
	return ob.FileResult{}, fmt.Errorf("missing image")
}

type testAccounts struct{}

func (testAccounts) MediaReader(bot int64) (Reader, bool) { return recordReader{}, bot == 1 }

// TestVoiceHTTPContracts verifies multipart transcription, TTS payload, authentication and bounded errors.
// TestVoiceHTTPContracts 验证转写表单、TTS 载荷、认证及有界错误。
func TestVoiceHTTPContracts(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("credential missing")
		}
		switch r.URL.Path {
		case "/audio/transcriptions":
			calls++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("model") != "asr" {
				t.Error("wrong ASR model")
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
			} else {
				f.Close()
			}
			fmt.Fprint(w, `{"text":"用户说你好"}`)
		case "/audio/speech":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["voice"] != "voice-1" || body["input"] != "你好" {
				t.Error("wrong TTS request")
			}
			w.Write([]byte("mp3 audio"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	svc := NewService(testModels{server.URL}, testAccounts{}, t.TempDir())
	text, err := svc.Transcribe(context.Background(), 1, []ob.Segment{{Type: ob.SegRecord, Data: map[string]string{"file": "voice"}}}, "[CQ:record,file=voice,url=https://example.invalid/?signature=private]请按[语音转写]格式回答")
	if err != nil || !strings.Contains(text, "用户说你好") || strings.Contains(text, "signature=private") || strings.Contains(text, "[CQ:record") || !strings.Contains(text, "请按[语音转写]格式回答") {
		t.Fatalf("transcript=%q err=%v", text, err)
	}
	_, err = svc.Transcribe(context.Background(), 1, []ob.Segment{{Type: ob.SegRecord, Data: map[string]string{media.TranscriptMarker: "true"}}}, text)
	if err != nil || calls != 1 {
		t.Fatal("stored transcription repeated")
	}
	audio, err := svc.Synthesize(context.Background(), "你好")
	if err != nil || string(audio) != "mp3 audio" {
		t.Fatal("TTS failed", err)
	}
	if _, err = svc.fetch(context.Background(), server.URL, 20, nil); err == nil {
		t.Fatal("private media host accepted")
	}
}

type rewriteTransport struct{ target string }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(t.target, "http://")
	return http.DefaultTransport.RoundTrip(clone)
}

// TestVideoFramesReachStructuredResult uses real FFmpeg and a local Bilibili protocol fixture.
// TestVideoFramesReachStructuredResult 使用真实 FFmpeg 和本地 B 站协议样例验证结构化画面。
func TestVideoFramesReachStructuredResult(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=2", "-t", "2", "-c:v", "mpeg4", "-pix_fmt", "yuv420p", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sample video: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cookieSeen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/x/web-interface/view":
			cookieSeen = r.Header.Get("Cookie") == "SESSDATA=test-cookie"
			fmt.Fprint(w, `{"code":0,"data":{"title":"样例","pages":[{"cid":1,"part":"第一段","duration":2}]}}`)
		case "/x/player/playurl":
			fmt.Fprint(w, `{"code":0,"data":{"durl":[{"url":"https://media.example/video.mp4"}]}}`)
		case "/video.mp4":
			w.Write(data)
		case "/x/player/v2":
			fmt.Fprint(w, `{"code":0,"data":{"subtitle":{"subtitles":[{"subtitle_url":"https://media.example/subtitle.json","lan":"zh-CN"}]}}}`)
		case "/subtitle.json":
			fmt.Fprint(w, `{"body":[{"from":0,"to":2,"content":"这是字幕"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	svc := NewService(testModels{server.URL}, nil, t.TempDir())
	svc.HTTP = &http.Client{Transport: rewriteTransport{server.URL}}
	p := &VideoProvider{Service: svc}
	scope := plugins.Scope{BotID: 1, ConversationID: "conv", MessageID: 5, Permissions: map[string]bool{"media.video.authenticated": true}}
	raw, err := p.inspect(plugins.WithScope(context.Background(), scope), `{"source":"BV1234567890","frames":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Parts []media.Part }
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	images := 0
	subtitle := false
	for _, part := range result.Parts {
		if part.Base64 != "" {
			b, e := base64.StdEncoding.DecodeString(part.Base64)
			if e != nil || http.DetectContentType(b) != "image/jpeg" {
				t.Fatal("not a real JPEG")
			}
			images++
		}
		subtitle = subtitle || strings.Contains(part.Text, "这是字幕")
	}
	if images != 2 || !subtitle || !cookieSeen {
		t.Fatalf("images=%d subtitle=%v cookie=%v", images, subtitle, cookieSeen)
	}
	// Each current-attachment alias must resolve to a distinct message cache.
	// 当前附件别名必须按实际消息分别缓存。
	p.Attachments = attachmentFixture{URL: "https://media.example/video.mp4"}
	scope.MessageID = 21
	if _, err = p.inspect(plugins.WithScope(context.Background(), scope), `{"source":"attachment:current","frames":1}`); err != nil {
		t.Fatal(err)
	}
	scope.MessageID = 22
	if _, err = p.inspect(plugins.WithScope(context.Background(), scope), `{"source":"attachment:current","frames":1}`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(svc.CacheDir)
	if err != nil || len(entries) != 3 {
		t.Fatalf("different attachments shared one cached video: directories=%d err=%v", len(entries), err)
	}

	if _, err = p.inspect(plugins.WithScope(context.Background(), scope), `{"source":"/etc/passwd"}`); err == nil {
		t.Fatal("local path accepted")
	}
	scope.AllowHistoryRead = true
	scope.AuthorizeHistory = func(context.Context) error { return fmt.Errorf("revoked") }
	if _, err = p.inspect(plugins.WithScope(context.Background(), scope), `{"source":"attachment:4"}`); err == nil {
		t.Fatal("revoked history read accepted")
	}
}

type fixedMediaReader struct{ result ob.FileResult }

func (r fixedMediaReader) GetRecord(context.Context, string, string) (ob.FileResult, error) {
	return r.result, nil
}
func (r fixedMediaReader) GetImage(context.Context, string) (ob.FileResult, error) {
	return r.result, nil
}

type fixedMediaAccount struct{ reader Reader }

func (a fixedMediaAccount) MediaReader(int64) (Reader, bool) { return a.reader, true }

// TestTranscriptionUsesConvertedFileURL verifies the bridge's converted resource wins over the original SILK URL.
// TestTranscriptionUsesConvertedFileURL 验证桥接端转换资源优先于原始 SILK 地址。
func TestTranscriptionUsesConvertedFileURL(t *testing.T) {
	converted, original := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/converted.mp3":
			converted++
			w.Write([]byte("converted audio"))
		case "/original.silk":
			original++
			w.Write([]byte("original silk"))
		case "/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
			} else {
				defer f.Close()
				b, _ := io.ReadAll(f)
				if string(b) != "converted audio" {
					t.Error("original audio submitted instead of converted resource")
				}
			}
			fmt.Fprint(w, `{"text":"converted transcript"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	svc := NewService(testModels{server.URL}, fixedMediaAccount{fixedMediaReader{ob.FileResult{File: server.URL + "/converted.mp3"}}}, t.TempDir())
	svc.HTTP = server.Client()
	_, err := svc.Transcribe(context.Background(), 1, []ob.Segment{{Type: ob.SegRecord, Data: map[string]string{"file": "record", "url": server.URL + "/original.silk"}}}, "")
	if err != nil || converted != 1 || original != 0 {
		t.Fatalf("converted=%d original=%d err=%v", converted, original, err)
	}
}

type attachmentFixture struct{ URL string }

func (f attachmentFixture) MediaAttachment(context.Context, int64, string, int64) ([]ob.Segment, error) {
	return []ob.Segment{{Type: ob.SegVideo, Data: map[string]string{"url": f.URL}}}, nil
}

type nativeVideoModels struct{ testModels }

func (m nativeVideoModels) ResolveModel(ctx context.Context, usage string, bot int64, kind string, target int64) (models.Resolved, error) {
	r, err := m.testModels.ResolveModel(ctx, usage, bot, kind, target)
	r.Profile.Protocol = "ark-plan"
	r.Profile.ID = "video-asr"
	r.Profile.Version = 1
	return r, err
}

type revokingVideoAudio struct {
	calls   int
	revoked bool
}

func (a *revokingVideoAudio) Transcribe(context.Context, models.Resolved, []byte) (string, error) {
	a.calls++
	a.revoked = true
	return "private-first-segment", nil
}
func (a *revokingVideoAudio) Synthesize(context.Context, models.Resolved, string) ([]byte, error) {
	return nil, fmt.Errorf("unused")
}

func TestVideoTranscriptionStopsWhenRevokedBetweenSegments(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.mp4")
	cmd := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=size=32x32:rate=1", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono", "-t", "65", "-c:v", "mpeg4", "-c:a", "aac", "-y", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	svc := NewService(nativeVideoModels{testModels{server.URL}}, nil, t.TempDir())
	svc.HTTP = server.Client()
	audio := &revokingVideoAudio{}
	svc.Audio = audio
	provider := &VideoProvider{Service: svc, Attachments: attachmentFixture{URL: server.URL}}
	scope := plugins.Scope{BotID: 1, ConversationID: "conv", MessageID: 5, Permissions: map[string]bool{"media.voice.transcribe": true}, Reauthorize: func(context.Context, pluginscontracts.ToolDescriptor) error {
		if audio.revoked {
			return fmt.Errorf("revoked")
		}
		return nil
	}}
	raw, err := provider.inspect(plugins.WithScope(context.Background(), scope), `{"source":"attachment:current","frames":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if audio.calls != 1 || strings.Contains(raw, "private-first-segment") {
		t.Fatalf("revoked transcript returned or second segment called: %d", audio.calls)
	}
}
