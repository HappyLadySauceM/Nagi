package infrastructure

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/gorilla/websocket"
)

func TestArkTTSChunksAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "truncated", "business", "bad-base64"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "private" || r.Header.Get("X-Api-Resource-Id") != "seed-tts-2.0" || r.Header.Get("X-Custom") != "custom" {
					t.Error("missing headers")
				}
				var body struct {
					Req map[string]any `json:"req_params"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.Req["speaker"] != "voice-id" || body.Req["text"] != "你好" {
					t.Error("wrong payload")
				}
				enc := json.NewEncoder(w)
				if mode == "business" {
					_ = enc.Encode(map[string]any{"code": 45000000, "message": "secret upstream body"})
					return
				}
				if mode == "bad-base64" {
					_ = enc.Encode(map[string]any{"code": 0, "data": "!"})
					return
				}
				for _, part := range []string{"ID3first", "second"} {
					_ = enc.Encode(map[string]any{"code": 0, "data": base64.StdEncoding.EncodeToString([]byte(part))})
				}
				if mode != "truncated" {
					_ = enc.Encode(map[string]any{"code": 20000000})
				}
			}))
			defer server.Close()
			m := models.Resolved{APIKey: "private", Profile: models.Profile{BaseURL: server.URL, Voice: "voice-id", HTTPHeaders: map[string]string{"X-Custom": "custom"}}}
			b, err := (ArkAudio{}).Synthesize(context.Background(), m, "你好")
			if mode == "success" {
				if err != nil || string(b) != "ID3firstsecond" {
					t.Fatalf("unexpected result: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe success/error")
			}
		})
	}
}

func TestArkASRFramesAndUpload(t *testing.T) {
	const (
		audioSeconds = 65
		sampleRate   = 8000
		pcmSize      = audioSeconds * sampleRate * 2
	)
	// Generate 8 kHz mono PCM WAV in memory and let Transcribe resample it.
	// 在内存中生成 8 kHz 单声道 PCM WAV，由 Transcribe 完成重采样。
	audio := make([]byte, 44+pcmSize)
	le := binary.LittleEndian
	copy(audio[0:4], "RIFF")
	le.PutUint32(audio[4:8], uint32(len(audio)-8))
	copy(audio[8:16], "WAVEfmt ")
	le.PutUint32(audio[16:20], 16)
	le.PutUint16(audio[20:22], 1) // PCM
	le.PutUint16(audio[22:24], 1) // Mono / 单声道
	le.PutUint32(audio[24:28], sampleRate)
	le.PutUint32(audio[28:32], sampleRate*2)
	le.PutUint16(audio[32:34], 2)
	le.PutUint16(audio[34:36], 16)
	copy(audio[36:40], "data")
	le.PutUint32(audio[40:44], pcmSize)

	upgrade := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "private" || r.Header.Get("X-Api-Resource-Id") != "volc.seedasr.sauc.duration" {
			t.Error("missing ASR authentication")
		}
		c, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, frame, err := c.ReadMessage()
		if err != nil {
			return
		}
		if frame[1] != 0x11 {
			t.Error("bad initialization frame")
		}
		_ = c.WriteMessage(websocket.BinaryMessage, arkFrame(9, 1, 1, []byte(`{"result":{}}`)))
		for frames := 1; ; frames++ {
			_, frame, err = c.ReadMessage()
			if err != nil {
				return
			}
			if frame[1]>>4 != 2 {
				t.Error("not audio frame")
			}
			if frame[1]&2 != 0 {
				if int32(binary.BigEndian.Uint32(frame[4:8])) >= 0 {
					t.Error("last sequence must be negative")
				}
				if frames != audioSeconds*5 {
					t.Errorf("received %d audio frames, want %d", frames, audioSeconds*5)
				}
				break
			}
		}
		_ = c.WriteMessage(websocket.BinaryMessage, arkFrame(9, 3, -2, []byte(`{"result":{"text":"你好。"}}`)))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	result, err := (ArkAudio{}).Transcribe(ctx, models.Resolved{APIKey: "private", Profile: models.Profile{BaseURL: "ws" + strings.TrimPrefix(server.URL, "http")}}, audio)
	if err != nil || result != "你好。" {
		t.Fatalf("recognition failed: %v", err)
	}
	good := arkFrame(9, 3, -2, []byte(`{"result":{"text":"你好"}}`))
	for _, frame := range [][]byte{nil, {0x11}, {0x11, 0x93, 0x11, 0}, good[:len(good)-1], {0x11, 0xf0, 0x10, 0, 0, 0, 0, 1}} {
		if _, _, err := parseArkFrame(frame); err == nil {
			t.Fatal("malformed frame accepted")
		}
	}
}

func TestArkAudioLive(t *testing.T) {
	key := os.Getenv("NAGI_TEST_ARK_PLAN_KEY")
	if key == "" {
		t.Skip("requires Agent Plan credential")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	tts := models.Resolved{APIKey: key, Profile: models.Profile{Protocol: "ark-plan", Usage: "tts", BaseURL: "https://openspeech.bytedance.com/api/v3/plan/tts/unidirectional", Model: "seed-tts-2.0", Voice: "zh_female_gaolengyujie_uranus_bigtts"}}
	audio, err := (ArkAudio{}).Synthesize(ctx, tts, "你好，这是语音测试。")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(audio, []byte("ID3")) && !(len(audio) > 2 && audio[0] == 0xff) {
		t.Fatal("not MP3")
	}
	asr := models.Resolved{APIKey: key, Profile: models.Profile{Protocol: "ark-plan", Usage: "asr", BaseURL: "wss://openspeech.bytedance.com/api/v3/plan/sauc/bigmodel_nostream", Model: "doubao-seed-asr-2.0"}}
	text, err := (ArkAudio{}).Transcribe(ctx, asr, audio)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "语音测试") {
		t.Fatalf("unexpected transcription: %q", text)
	}
	if err := (HTTPTester{}).TestModel(ctx, tts); err != nil {
		t.Fatal(err)
	}
	if err := (HTTPTester{}).TestModel(ctx, asr); err != nil {
		t.Fatal(err)
	}
	t.Log("live synthesis, transcription and both save probes passed")
}
