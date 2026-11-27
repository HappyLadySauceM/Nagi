//go:build integration

package infrastructure

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
)

// TestArkAudioLive exercises real Ark Plan TTS/ASR and save probes.
// TestArkAudioLive 使用真实 Ark Plan TTS/ASR 与存盘探针。
func TestArkAudioLive(t *testing.T) {
	key := os.Getenv("NAGI_TEST_ARK_PLAN_KEY")
	if key == "" {
		t.Fatal("NAGI_TEST_ARK_PLAN_KEY is required for integration test")
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
