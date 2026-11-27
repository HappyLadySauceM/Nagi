//go:build integration

package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

type attachmentFixture struct{ URL string }

func (f attachmentFixture) MediaAttachment(context.Context, int64, string, int64) ([]ob.Segment, error) {
	return []ob.Segment{{Type: ob.SegVideo, Data: map[string]string{"url": f.URL}}}, nil
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

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Fatal("ffprobe is not installed")
	}
}

// TestVideoFramesReachStructuredResult uses real FFmpeg and a local Bilibili protocol fixture.
// TestVideoFramesReachStructuredResult 使用真实 FFmpeg 和本地 B 站协议样例验证结构化画面。
func TestVideoFramesReachStructuredResult(t *testing.T) {
	requireFFmpeg(t)
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

func TestVideoTranscriptionStopsWhenRevokedBetweenSegments(t *testing.T) {
	requireFFmpeg(t)
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
