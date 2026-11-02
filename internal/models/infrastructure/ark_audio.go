package infrastructure

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	"github.com/HappyLadySauceM/Nagi/pkg/modelhttp"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const arkAudioLimit = 25 << 20

// ArkAudio implements the Agent Plan HTTP synthesis and binary WebSocket recognition protocols.
// ArkAudio 实现 Agent Plan HTTP 合成及二进制 WebSocket 识别协议。
type ArkAudio struct {
	Client *http.Client
	Dialer *websocket.Dialer
}

type audioBuffer struct {
	bytes.Buffer
	limit int
}

func (b *audioBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("音频超过处理上限")
	}
	return b.Buffer.Write(p)
}

func arkHeaders(m models.Resolved, resource string) http.Header {
	h := http.Header{}
	h.Set("X-Api-Key", m.APIKey)
	h.Set("X-Api-Resource-Id", resource)
	h.Set("X-Api-Connect-Id", uuid.NewString())
	for k, v := range m.Profile.HTTPHeaders {
		h.Set(k, v)
	}
	return h
}

func (a ArkAudio) Synthesize(ctx context.Context, m models.Resolved, text string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	body, _ := json.Marshal(map[string]any{"user": map[string]string{"uid": "nagi"}, "req_params": map[string]any{"text": text, "speaker": m.Profile.Voice, "audio_params": map[string]any{"format": "mp3", "sample_rate": 24000}}})
	req, err := http.NewRequestWithContext(ctx, "POST", m.Profile.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("语音合成地址无效")
	}
	req.Header = arkHeaders(m, "seed-tts-2.0")
	req.Header.Set("Content-Type", "application/json")
	resp, err := modelhttp.Client(a.Client, nil).Do(req)
	if err != nil {
		return nil, fmt.Errorf("火山语音合成连接失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("火山语音合成返回 HTTP %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 40<<20))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	var audio []byte
	finished := false
	for scanner.Scan() {
		var item struct {
			Code int    `json:"code"`
			Data string `json:"data"`
		}
		if json.Unmarshal(scanner.Bytes(), &item) != nil {
			return nil, fmt.Errorf("火山语音合成响应格式无效")
		}
		if item.Code == 20000000 {
			finished = true
			break
		}
		if item.Code != 0 {
			return nil, fmt.Errorf("火山语音合成业务错误（%d）", item.Code)
		}
		chunk, err := base64.StdEncoding.DecodeString(item.Data)
		if err != nil || len(audio)+len(chunk) > arkAudioLimit {
			return nil, fmt.Errorf("火山语音合成音频无效或过大")
		}
		audio = append(audio, chunk...)
	}
	if scanner.Err() != nil || !finished || len(audio) < 4 {
		return nil, fmt.Errorf("火山语音合成响应不完整")
	}
	return audio, nil
}

func arkFrame(kind, flags byte, seq int32, payload []byte) []byte {
	var zipped bytes.Buffer
	w := gzip.NewWriter(&zipped)
	_, _ = w.Write(payload)
	_ = w.Close()
	b := make([]byte, 12+zipped.Len())
	b[0] = 0x11
	b[1] = kind<<4 | flags
	b[2] = 0x11
	binary.BigEndian.PutUint32(b[4:8], uint32(seq))
	binary.BigEndian.PutUint32(b[8:12], uint32(zipped.Len()))
	copy(b[12:], zipped.Bytes())
	return b
}

func parseArkFrame(frame []byte) (string, bool, error) {
	bad := fmt.Errorf("火山语音识别响应格式无效")
	if len(frame) < 4 || frame[0]>>4 != 1 {
		return "", false, bad
	}
	offset := int(frame[0]&15) * 4
	if offset < 4 || offset > len(frame) {
		return "", false, bad
	}
	flags := frame[1] & 15
	kind := frame[1] >> 4
	if flags&1 != 0 {
		offset += 4
	}
	if flags&4 != 0 {
		offset += 4
	}
	if offset+4 > len(frame) {
		return "", false, bad
	}
	if kind == 15 {
		return "", false, fmt.Errorf("火山语音识别业务错误（%d）", binary.BigEndian.Uint32(frame[offset:offset+4]))
	}
	if kind != 9 {
		return "", false, bad
	}
	size := int(binary.BigEndian.Uint32(frame[offset : offset+4]))
	offset += 4
	if size != len(frame)-offset {
		return "", false, bad
	}
	payload := frame[offset:]
	if frame[2]&15 == 1 {
		r, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return "", false, bad
		}
		payload, err = io.ReadAll(io.LimitReader(r, (1<<20)+1))
		r.Close()
		if err != nil || len(payload) > 1<<20 {
			return "", false, bad
		}
	} else if frame[2]&15 != 0 {
		return "", false, bad
	}
	var result struct {
		Code   int `json:"code"`
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	if frame[2]>>4 != 1 || json.Unmarshal(payload, &result) != nil {
		return "", false, bad
	}
	if result.Code != 0 && result.Code != 20000000 {
		return "", false, fmt.Errorf("火山语音识别业务错误（%d）", result.Code)
	}
	return result.Result.Text, flags&2 != 0, nil
}

func (a ArkAudio) Transcribe(ctx context.Context, m models.Resolved, audio []byte) (string, error) {
	if len(audio) == 0 || len(audio) > arkAudioLimit {
		return "", fmt.Errorf("待识别音频为空或过大")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", "pipe:0", "-f", "s16le", "-ar", "16000", "-ac", "1", "pipe:1")
	cmd.Stdin = bytes.NewReader(audio)
	pcm := &audioBuffer{limit: 8 << 20}
	cmd.Stdout = pcm
	if cmd.Run() != nil || pcm.Len() == 0 {
		return "", fmt.Errorf("语音识别音频转码失败或过大")
	}
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	if a.Dialer != nil {
		dialer = *a.Dialer
	}
	conn, resp, err := dialer.DialContext(ctx, m.Profile.BaseURL, arkHeaders(m, "volc.seedasr.sauc.duration"))
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return "", fmt.Errorf("火山语音识别握手失败（HTTP %d）", resp.StatusCode)
		}
		return "", fmt.Errorf("火山语音识别连接失败或超时")
	}
	defer conn.Close()
	conn.SetReadLimit(2 << 20)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		_ = conn.SetWriteDeadline(deadline)
	}
	payload, _ := json.Marshal(map[string]any{"user": map[string]string{"uid": "nagi"}, "audio": map[string]any{"format": "pcm", "codec": "raw", "rate": 16000, "bits": 16, "channel": 1}, "request": map[string]any{"model_name": "bigmodel", "enable_itn": true, "enable_punc": true, "show_utterances": true}})
	if conn.WriteMessage(websocket.BinaryMessage, arkFrame(1, 1, 1, payload)) != nil {
		return "", fmt.Errorf("火山语音识别初始化失败")
	}
	_, ack, err := conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("火山语音识别初始化响应失败")
	}
	if _, _, err = parseArkFrame(ack); err != nil {
		return "", err
	}
	sender := make(chan error, 1)
	go func() {
		data := pcm.Bytes()
		seq := int32(2)
		for offset := 0; offset < len(data); offset += 6400 {
			end := offset + 6400
			if end > len(data) {
				end = len(data)
			}
			flags := byte(1)
			n := seq
			if end == len(data) {
				flags = 3
				n = -seq
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, arkFrame(2, flags, n, data[offset:end])); err != nil {
				sender <- err
				conn.Close()
				return
			}
			seq++
		}
		sender <- nil
	}()
	defer func() { cancel(); conn.Close(); <-sender }()
	var text string
	for {
		kind, frame, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("火山语音识别响应中断或超时")
		}
		if kind != websocket.BinaryMessage {
			return "", fmt.Errorf("火山语音识别响应类型无效")
		}
		value, last, err := parseArkFrame(frame)
		if err != nil {
			return "", err
		}
		if value != "" {
			text = value
		}
		if last {
			if strings.TrimSpace(text) == "" {
				return "", fmt.Errorf("语音识别未返回有效文本")
			}
			return text, nil
		}
	}
}
