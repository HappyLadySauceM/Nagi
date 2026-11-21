package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const configLimit = 16 << 10

type filterConfig struct {
	Listen          string `json:"listen"`
	Upstream        string `json:"upstream"`
	UpstreamToken   string `json:"upstream_token"`
	DownstreamToken string `json:"downstream_token"`
	StateDir        string `json:"state_dir"`
	BotID           int64  `json:"bot_id"`
	Kind            string `json:"kind"`
	TargetID        int64  `json:"target_id"`
	SenderID        int64  `json:"sender_id"`
	Trigger         string `json:"trigger"`
}

func main() {
	path := flag.String("config", "", "path to an authorized filter config")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if flag.NArg() != 0 || serve(ctx, *path) != nil {
		log.Print("onebot filter stopped with an error")
		os.Exit(1)
	}
}

func serve(ctx context.Context, path string) error {
	cfg, digest, err := loadConfig(path)
	if err != nil {
		return err
	}
	if err := claim(cfg.StateDir, digest); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	proxy := &filter{cfg: cfg, ctx: ctx}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 5 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	log.Print("onebot filter listening")
	err = server.Serve(listener)
	cancel()
	proxy.wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func loadConfig(path string) (filterConfig, [32]byte, error) {
	var cfg filterConfig
	var digest [32]byte
	f, err := os.Open(path)
	if err != nil {
		return cfg, digest, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return cfg, digest, fmt.Errorf("config must be a private regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, configLimit+1))
	if err != nil || len(raw) > configLimit || object(raw) == nil {
		return cfg, digest, fmt.Errorf("invalid config object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil || !cfg.valid() {
		return cfg, digest, fmt.Errorf("invalid filter config")
	}
	return cfg, sha256.Sum256(raw), nil
}

func (cfg filterConfig) valid() bool {
	host, _, err := net.SplitHostPort(cfg.Listen)
	u, urlErr := url.Parse(cfg.Upstream)
	return err == nil && net.ParseIP(host).IsLoopback() && urlErr == nil && u.Hostname() != "" &&
		(u.Scheme == "ws" || u.Scheme == "wss") && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		validToken(cfg.UpstreamToken) && validToken(cfg.DownstreamToken) && cfg.UpstreamToken != cfg.DownstreamToken &&
		filepath.IsAbs(cfg.StateDir) && cfg.BotID > 0 && cfg.SenderID > 0 && cfg.BotID != cfg.SenderID && cfg.TargetID > 0 &&
		(cfg.Kind == "group" || (cfg.Kind == "private" && cfg.TargetID == cfg.SenderID)) && validText(cfg.Trigger)
}

func validToken(s string) bool {
	return len(s) >= 16 && len(s) <= 256 && !strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r >= 127 })
}

// claim consumes startup authorization before any client or upstream connection.
// claim 在进程建立任何入站或上游连接前消耗启动授权。
func claim(dir string, digest [32]byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state directory must be private")
	}
	armed, err := root.Open("armed")
	if err != nil {
		return err
	}
	defer armed.Close()
	info, err = armed.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("invalid startup permit")
	}
	data, err := io.ReadAll(io.LimitReader(armed, 66))
	if err != nil || len(data) > 65 || strings.TrimSuffix(string(data), "\n") != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("startup permit does not match config")
	}
	used, err := root.OpenFile("claimed", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer used.Close()
	if err := used.Sync(); err != nil {
		return err
	}
	return directory.Sync()
}
