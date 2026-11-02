package infrastructure

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	ob "github.com/HappyLadySauceM/Nagi/internal/onebot/domain"
	plugins "github.com/HappyLadySauceM/Nagi/internal/plugins/application"
	tools "github.com/HappyLadySauceM/Nagi/internal/plugins/contracts"
)

type AttachmentReader interface {
	MediaAttachment(context.Context, int64, string, int64) ([]ob.Segment, error)
}
type VideoProvider struct {
	Service     *Service
	Attachments AttachmentReader
}

func (p *VideoProvider) Manifest() tools.Manifest {
	return tools.Manifest{ID: "dsh-video-vision", Version: "1", Description: "B站及QQ视频的画面、字幕与转写"}
}
func (p *VideoProvider) Close(context.Context) error { return nil }
func (p *VideoProvider) Tools(context.Context) ([]tools.RegisteredTool, error) {
	no := false
	return []tools.RegisteredTool{{Descriptor: tools.ToolDescriptor{ID: "media.video.inspect", Description: "dsh-video-vision：观看B站链接或QQ视频附件。source接受BV号、B站链接或attachment:current；支持start/end秒数定位，返回真实画面与字幕。不得传本地文件路径。", ReadOnly: true, Multimodal: true, Media: true, DefaultAllowed: &no, Permission: "media.video.read", AdditionalPermissions: []string{"media.video.authenticated"}, Category: "media", Parameters: map[string]tools.Parameter{"source": {Type: "string", Required: true, Description: "B站链接/BV号，或attachment:current（当前消息）"}, "start": {Type: "number", Description: "开始秒数，默认0"}, "end": {Type: "number", Description: "结束秒数，默认全片"}, "frames": {Type: "integer", Description: "画面数，默认6，最多12"}}}, Invoke: p.inspect}}, nil
}

type videoArgs struct {
	Source string  `json:"source"`
	Start  float64 `json:"start"`
	End    float64 `json:"end"`
	Frames int     `json:"frames"`
}
type videoInfo struct {
	URL      string
	AudioURL string
	Duration float64
	Title    string
	BVID     string
	CID      int64
	Cookie   string
}

var bvPattern = regexp.MustCompile(`(?i)^BV[0-9a-z]{10}$`)

func (p *VideoProvider) inspect(ctx context.Context, raw string) (string, error) {
	scope, ok := plugins.ScopeFrom(ctx)
	if !ok {
		return "", fmt.Errorf("video: scope required")
	}
	var args videoArgs
	if json.Unmarshal([]byte(raw), &args) != nil || args.Source == "" {
		return "", fmt.Errorf("invalid video arguments")
	}
	if args.Frames == 0 {
		args.Frames = 6
	}
	if args.Frames < 1 || args.Frames > 12 || args.Start < 0 || args.End < 0 || math.IsNaN(args.Start) || math.IsNaN(args.End) {
		return "", fmt.Errorf("invalid video range or frame count")
	}
	info := videoInfo{}
	cacheSource := args.Source
	var err error
	if strings.HasPrefix(args.Source, "attachment:") {
		id, e := strconv.ParseInt(strings.TrimPrefix(args.Source, "attachment:"), 10, 64)
		if args.Source == "attachment:current" {
			id = scope.MessageID
			e = nil
		}
		if e != nil || id == 0 || (id != scope.MessageID && !scope.AllowHistoryRead) {
			return "", fmt.Errorf("video attachment is not authorized")
		}
		cacheSource = fmt.Sprintf("attachment:%d", id)
		if id != scope.MessageID && scope.AuthorizeHistory != nil {
			if e := scope.AuthorizeHistory(ctx); e != nil {
				return "", e
			}
		}
		segments, e := p.Attachments.MediaAttachment(ctx, scope.BotID, scope.ConversationID, id)
		if e != nil {
			return "", fmt.Errorf("video attachment unavailable")
		}
		for _, seg := range segments {
			if seg.Type == ob.SegVideo {
				info.URL = seg.Data["url"]
				break
			}
		}
		if info.URL == "" {
			return "", fmt.Errorf("video attachment has no download URL")
		}
		info.Title = "QQ视频附件"
	} else {
		authenticated := scope.Permissions["media.video.authenticated"]
		if authenticated {
			if err := plugins.RequirePermission(ctx, "media.video.authenticated"); err != nil {
				return "", err
			}
		}
		info, err = p.Service.bilibili(ctx, args.Source, authenticated)
		if err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s:%t", scope.BotID, scope.ConversationID, cacheSource, info.Cookie != "")))
	dir := filepath.Join(p.Service.CacheDir, hex.EncodeToString(sum[:]))
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("video cache unavailable")
	}
	p.Service.pruneCache(dir)
	video := filepath.Join(dir, "video.mp4")
	if infoMod, e := os.Stat(video); e != nil || time.Since(infoMod.ModTime()) > 24*time.Hour {
		if err = p.Service.download(ctx, info.URL, video, bilibiliHeaders()); err != nil {
			return "", err
		}
	}
	duration, err := probe(ctx, video)
	if err != nil {
		return "", err
	}
	end := args.End
	if end == 0 || end > duration {
		end = duration
	}
	if args.Start >= end {
		return "", fmt.Errorf("video range is outside duration")
	}
	parts := []media.Part{{Text: fmt.Sprintf("视频：%s；时长 %.2f秒；本次画面范围 %.2f–%.2f秒。画面为采样，不能证明采样间发生的全部内容。", info.Title, duration, args.Start, end)}}
	for i := 0; i < args.Frames; i++ {
		at := args.Start + (end-args.Start)*float64(i)/float64(args.Frames)
		frame := filepath.Join(dir, fmt.Sprintf("frame-%0.3f.jpg", at))
		if _, e := os.Stat(frame); e != nil {
			cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-ss", fmt.Sprintf("%.3f", at), "-protocol_whitelist", "file,pipe", "-f", "mov", "-i", video, "-frames:v", "1", "-vf", "scale=1024:1024:force_original_aspect_ratio=decrease", "-q:v", "3", "-y", frame)
			if err = cmd.Run(); err != nil {
				return "", fmt.Errorf("video frame extraction failed; check FFmpeg")
			}
		}
		b, e := os.ReadFile(frame)
		if e != nil || len(b) > 2<<20 {
			return "", fmt.Errorf("video frame unavailable or too large")
		}
		parts = append(parts, media.Part{Text: fmt.Sprintf("画面时间戳 %.3f秒", at)}, media.Part{MIME: "image/jpeg", Base64: base64.StdEncoding.EncodeToString(b)})
	}
	transcriptEnd := math.Min(end, args.Start+180)
	subtitles, err := p.Service.subtitles(ctx, info, args.Start, transcriptEnd)
	if err == nil && subtitles != "" {
		parts = append(parts, media.Part{Text: subtitles})
	} else if permissionErr := plugins.RequirePermission(ctx, "media.voice.transcribe"); permissionErr != nil {
		parts = append(parts, media.Part{Text: "无平台字幕；未授权语音识别，未执行转写。"})
	} else {
		audioSource := video
		if info.AudioURL != "" {
			audioSource = filepath.Join(dir, "audio.m4a")
			if _, e := os.Stat(audioSource); e != nil {
				if e = p.Service.download(ctx, info.AudioURL, audioSource, bilibiliHeaders()); e != nil {
					audioSource = ""
				}
			}
		}
		var transcript strings.Builder
		if audioSource != "" {
			for start := args.Start; start < transcriptEnd; start += 60 {
				if plugins.RequirePermission(ctx, "media.voice.transcribe") != nil {
					transcript.Reset()
					break
				}
				stop := math.Min(start+60, transcriptEnd)
				audio := filepath.Join(dir, fmt.Sprintf("clip-%.3f-%.3f.mp3", start, stop))
				cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-ss", fmt.Sprintf("%.3f", start), "-protocol_whitelist", "file,pipe", "-f", "mov", "-i", audioSource, "-t", fmt.Sprintf("%.3f", stop-start), "-vn", "-ac", "1", "-ar", "16000", "-y", audio)
				if cmd.Run() != nil {
					break
				}
				b, e := os.ReadFile(audio)
				if e != nil || len(b) > maxAudioBytes {
					break
				}
				textPath := audio + ".txt"
				cacheKey := ""
				if p.Service.Models != nil {
					if resolved, e := p.Service.Models.ResolveModel(ctx, "asr", 0, "", 0); e == nil {
						cacheKey = fmt.Sprintf("%s:%d", resolved.Profile.ID, resolved.Profile.Version)
					}
				}
				if plugins.RequirePermission(ctx, "media.voice.transcribe") != nil {
					transcript.Reset()
					break
				}
				cached, _ := os.ReadFile(textPath)
				text := ""
				if strings.HasPrefix(string(cached), cacheKey+"\n") && cacheKey != "" {
					text = strings.TrimPrefix(string(cached), cacheKey+"\n")
				} else {
					text, e = p.Service.transcribeAudio(ctx, b)
					if plugins.RequirePermission(ctx, "media.voice.transcribe") != nil {
						transcript.Reset()
						break
					}
					if e == nil && cacheKey != "" {
						_ = os.WriteFile(textPath, []byte(cacheKey+"\n"+text), 0600)
					}
				}
				if e != nil {
					break
				}
				fmt.Fprintf(&transcript, "[%.2f–%.2f秒，片段转写] %s\n", start, stop, text)
			}
		}
		if plugins.RequirePermission(ctx, "media.voice.transcribe") != nil {
			transcript.Reset()
		}
		if transcript.Len() > 0 {
			parts = append(parts, media.Part{Text: transcript.String()})
		} else {
			parts = append(parts, media.Part{Text: "本次未取得可用字幕或语音转写，仅依据所返回画面分析。"})
		}
	}
	result, err := json.Marshal(struct{ Parts []media.Part }{parts})
	return string(result), err
}

func probe(ctx context.Context, path string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-f", "mov", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("video probe failed; check ffprobe")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || n <= 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, fmt.Errorf("invalid video duration")
	}
	return n, nil
}
func (s *Service) download(ctx context.Context, raw, path string, headers map[string]string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return fmt.Errorf("invalid download URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return fmt.Errorf("invalid download request")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("video download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("video unavailable (%d)", resp.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return fmt.Errorf("video cache unavailable")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, (512<<20)+1))
	if err != nil {
		return fmt.Errorf("video download interrupted")
	}
	if n > 512<<20 {
		return fmt.Errorf("video exceeds 512 MiB limit")
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (s *Service) pruneCache(current string) {
	entries, _ := os.ReadDir(s.CacheDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(s.CacheDir, e.Name())
		if path == current {
			continue
		}
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(path)
		}
	}
}

func (s *Service) biliAPI(ctx context.Context, path string, q url.Values, cookie string, out any) error {
	headers := bilibiliHeaders()
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	b, err := s.fetch(ctx, "https://api.bilibili.com"+path+"?"+q.Encode(), 4<<20, headers)
	if err != nil {
		return err
	}
	var result struct {
		Code int
		Data json.RawMessage
	}
	if json.Unmarshal(b, &result) != nil {
		return fmt.Errorf("invalid Bilibili response")
	}
	switch result.Code {
	case 0:
	case -101:
		return mediaFailure("media_login_required", "B站登录已失效或该资源需要登录")
	case -403, -10403:
		return mediaFailure("media_access_denied", "B站账号没有访问此视频的权限")
	case -404, 62002:
		return mediaFailure("media_unavailable", "B站视频不存在或已不可用")
	default:
		return fmt.Errorf("Bilibili request failed (%d)", result.Code)
	}
	if json.Unmarshal(result.Data, out) != nil {
		return fmt.Errorf("invalid Bilibili data")
	}
	return nil
}
func (s *Service) bilibili(ctx context.Context, source string, authenticated bool) (videoInfo, error) {
	info := videoInfo{}
	page := 1
	source = strings.TrimSpace(source)
	if !bvPattern.MatchString(source) {
		u, err := url.Parse(source)
		if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.User != nil {
			return info, fmt.Errorf("only Bilibili links or BV identifiers are supported")
		}
		if u.Hostname() == "b23.tv" {
			req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
			for k, v := range bilibiliHeaders() {
				req.Header.Set(k, v)
			}
			resp, e := s.HTTP.Do(req)
			if e != nil {
				return info, fmt.Errorf("Bilibili short link unavailable")
			}
			resp.Body.Close()
			u = resp.Request.URL
		}
		if u.Hostname() != "www.bilibili.com" && u.Hostname() != "bilibili.com" && u.Hostname() != "m.bilibili.com" {
			return info, fmt.Errorf("only Bilibili links are supported")
		}
		for _, part := range strings.Split(u.Path, "/") {
			if bvPattern.MatchString(part) {
				source = part
				break
			}
		}
		if raw := u.Query().Get("p"); raw != "" {
			page, err = strconv.Atoi(raw)
			if err != nil || page < 1 {
				return info, fmt.Errorf("invalid Bilibili part")
			}
		}
	}
	if !bvPattern.MatchString(source) {
		return info, fmt.Errorf("Bilibili BV identifier required")
	}
	source = "BV" + source[2:]
	cookie := ""
	if authenticated {
		var err error
		cookie, err = s.Models.BilibiliCredential(ctx)
		if err != nil {
			return info, err
		}
	}
	var view struct {
		Title string
		Pages []struct {
			CID      int64
			Part     string
			Duration float64
		}
	}
	if err := s.biliAPI(ctx, "/x/web-interface/view", url.Values{"bvid": {source}}, cookie, &view); err != nil {
		return info, err
	}
	if page > len(view.Pages) {
		return info, fmt.Errorf("Bilibili part does not exist")
	}
	part := view.Pages[page-1]
	var playback struct {
		Durl []struct{ URL string }
		Dash struct {
			Video []struct {
				BaseURL string `json:"baseUrl"`
				Height  int
			}
			Audio []struct {
				BaseURL string `json:"baseUrl"`
			}
		}
	}
	if err := s.biliAPI(ctx, "/x/player/playurl", url.Values{"bvid": {source}, "cid": {strconv.FormatInt(part.CID, 10)}, "qn": {"32"}, "fnval": {"4048"}}, cookie, &playback); err != nil {
		return info, err
	}
	if len(playback.Dash.Video) > 0 {
		best := playback.Dash.Video[0]
		for _, v := range playback.Dash.Video {
			if v.Height < best.Height {
				best = v
			}
		}
		info.URL = best.BaseURL
		if len(playback.Dash.Audio) > 0 {
			info.AudioURL = playback.Dash.Audio[0].BaseURL
		}
	} else if len(playback.Durl) == 1 {
		info.URL = playback.Durl[0].URL
	}
	if info.URL == "" {
		return info, fmt.Errorf("Bilibili playback unavailable; login or access rights may be required")
	}
	info.Title = view.Title + " / " + part.Part
	info.Duration = part.Duration
	info.BVID = source
	info.CID = part.CID
	info.Cookie = cookie
	return info, nil
}
func (s *Service) subtitles(ctx context.Context, info videoInfo, start, end float64) (string, error) {
	if info.BVID == "" {
		return "", nil
	}
	var player struct {
		Subtitle struct {
			Subtitles []struct {
				URL      string `json:"subtitle_url"`
				Language string `json:"lan"`
			}
		}
	}
	if err := s.biliAPI(ctx, "/x/player/v2", url.Values{"bvid": {info.BVID}, "cid": {strconv.FormatInt(info.CID, 10)}}, info.Cookie, &player); err != nil {
		return "", err
	}
	if len(player.Subtitle.Subtitles) == 0 {
		return "", nil
	}
	selected := player.Subtitle.Subtitles[0]
	for _, t := range player.Subtitle.Subtitles {
		if strings.HasPrefix(t.Language, "zh") {
			selected = t
			break
		}
	}
	raw := selected.URL
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	b, err := s.fetch(ctx, raw, 4<<20, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Body []struct {
			From    float64
			To      float64
			Content string
		}
	}
	if json.Unmarshal(b, &result) != nil {
		return "", fmt.Errorf("invalid subtitle data")
	}
	var out strings.Builder
	fmt.Fprintf(&out, "平台字幕（覆盖 %.2f–%.2f秒）：\n", start, end)
	count := 0
	for _, cue := range result.Body {
		if cue.To < start || cue.From > end {
			continue
		}
		if out.Len()+len(cue.Content) > 48000 {
			break
		}
		fmt.Fprintf(&out, "[%.2f–%.2f秒] %s\n", cue.From, cue.To, cue.Content)
		count++
	}
	if count == 0 {
		return "", nil
	}
	return out.String(), nil
}

func bilibiliHeaders() map[string]string {
	return map[string]string{"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36", "Referer": "https://www.bilibili.com/", "Origin": "https://www.bilibili.com", "Accept": "*/*"}
}
