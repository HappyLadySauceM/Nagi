package infrastructure

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	const audioSeconds = 65
	mockPCM := make([]byte, audioSeconds*16000*2)

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
	ark := ArkAudio{
		PCMFrom: func(context.Context, []byte) ([]byte, error) { return mockPCM, nil },
	}
	result, err := ark.Transcribe(ctx, models.Resolved{APIKey: "private", Profile: models.Profile{BaseURL: "ws" + strings.TrimPrefix(server.URL, "http")}}, probeSpeech)
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
