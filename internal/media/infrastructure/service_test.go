package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	auditcontracts "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	auditdomain "github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
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

type synthesisAuditRecorder struct {
	auditcontracts.AIRecorder
	status string
}

func (r *synthesisAuditRecorder) RecordEvent(_ context.Context, event auditdomain.AIEvent) (auditdomain.AIEvent, error) {
	event.ID = "synthetic-speech"
	return event, nil
}

func (r *synthesisAuditRecorder) FinishEvent(ctx context.Context, _ string, status, _ string, _ []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.status = status
	return nil
}

// TestSynthesisFinishesAuditAfterHTTPCleanup covers the caller's live audit context.
// TestSynthesisFinishesAuditAfterHTTPCleanup 覆盖 HTTP 请求清理后的原运行审计上下文。
func TestSynthesisFinishesAuditAfterHTTPCleanup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "fixture audio")
	}))
	defer server.Close()
	recorder := &synthesisAuditRecorder{}
	svc := NewService(testModels{server.URL}, nil, t.TempDir())
	ctx := auditcontracts.WithRun(context.Background(), recorder, "synthetic-run")
	audio, err := svc.Synthesize(ctx, "测试语音")
	if err != nil || string(audio) != "fixture audio" || recorder.status != "succeeded" {
		t.Fatalf("audio=%q status=%q err=%v", audio, recorder.status, err)
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

type countingConsentArchive struct{ calls int }

func (a *countingConsentArchive) Archive(context.Context, media.ArchiveRequest) (media.ArchiveReference, error) {
	a.calls++
	return media.ArchiveReference{}, nil
}

func TestConsentDerivedSpeechAndArchiveDoNotDispatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("isolated")) }))
	defer server.Close()
	archiver := &countingConsentArchive{}
	svc := &Service{Models: testModels{server.URL}, Archiver: archiver}
	ctx := memory.WithRunReads(context.Background())
	memory.TrackVersionedRead(ctx, nil, memory.DetailRequest{}, memory.Memory{ConsentIdentity: "synthetic"}, nil)
	if _, err := svc.Synthesize(ctx, "synthetic derived audio"); !errors.Is(err, memory.ErrReadChanged) {
		t.Fatal(err)
	}
	if _, err := svc.archive(ctx, media.ArchiveRequest{Kind: "audio.outbound.synthesized", Data: []byte("synthetic")}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 || archiver.calls != 0 {
		t.Fatalf("TTS requests=%d archive calls=%d", requests.Load(), archiver.calls)
	}
}
