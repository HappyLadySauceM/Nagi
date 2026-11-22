package eino

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	memory "github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
)

// consentAwareModelTransport keeps the native media observer for ordinary requests.
// consentAwareModelTransport 为普通请求保留原生传输的媒体取证能力。
type consentAwareModelTransport struct {
	ordinary, derived http.RoundTripper
}

func (t consentAwareModelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if memory.CrossGroupReadUsed(req.Context()) {
		return t.derived.RoundTrip(req)
	}
	return t.ordinary.RoundTrip(req)
}

type guardedModelTransport struct {
	base      http.RoundTripper
	endpoint  *url.URL
	model     string
	dial      func(context.Context, string, string) (net.Conn, error)
	tlsConfig *tls.Config
}

type guardedGenerationConn struct {
	net.Conn
	first  sync.Once
	check  func() error
	denied error
}

func (c *guardedGenerationConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.first.Do(func() { c.denied = c.check() })
	if c.denied != nil {
		return 0, c.denied
	}
	return c.Conn.Write(p)
}

func (t guardedModelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := memory.ValidateRunReads(req.Context()); err != nil {
		return nil, err
	}
	request := req.Clone(req.Context())
	request.GetBody = nil
	if !memory.CrossGroupReadUsed(req.Context()) {
		return t.base.RoundTrip(request)
	}
	const maxDerivedBody = 32 << 20
	if request.Body == nil {
		return nil, memory.ErrReadChanged
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxDerivedBody+1))
	closeErr := request.Body.Close()
	var wire struct {
		Model string `json:"model"`
	}
	if err != nil || closeErr != nil || len(body) > maxDerivedBody || json.Unmarshal(body, &wire) != nil || wire.Model == "" || wire.Model != t.model {
		return nil, memory.ErrReadChanged
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	reads, local := memory.GenerationReads(req.Context())
	check := func() error {
		if err := request.Context().Err(); err != nil {
			return err
		}
		u := request.URL
		if t.endpoint == nil || u.Scheme != t.endpoint.Scheme || u.Host != t.endpoint.Host || u.User != nil ||
			(request.Host != "" && request.Host != t.endpoint.Host) || u.RawQuery != "" ||
			u.EscapedPath() != strings.TrimRight(t.endpoint.EscapedPath(), "/")+"/chat/completions" || request.Method != http.MethodPost {
			return memory.ErrReadChanged
		}
		return memory.ValidateConsentDestination(request.Context(), memory.GenerationDispatch{Endpoint: u.String(), Model: wire.Model, Reads: reads, LocalReads: local})
	}

	// A fresh HTTP/1 connection attributes the first application write to exactly this request.
	// 每笔使用独立 HTTP/1 连接，首次应用层写入只归属本次请求。
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	transport := &http.Transport{DisableKeepAlives: true, Protocols: protocols}
	var dialed atomic.Bool
	dialOnce := func(ctx context.Context, network, address string) (net.Conn, error) {
		if t.dial == nil || !dialed.CompareAndSwap(false, true) {
			return nil, memory.ErrReadChanged
		}
		return t.dial(ctx, network, address)
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialOnce(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &guardedGenerationConn{Conn: conn, check: check}, nil
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if t.endpoint.Scheme != "https" {
			return nil, memory.ErrReadChanged
		}
		conf := &tls.Config{MinVersion: tls.VersionTLS12}
		if t.tlsConfig != nil {
			conf = t.tlsConfig.Clone()
		}
		conf.ServerName = t.endpoint.Hostname()
		conf.NextProtos = []string{"http/1.1"}
		raw, err := dialOnce(ctx, network, address)
		if err != nil {
			return nil, err
		}
		conn := tls.Client(raw, conf)
		// A custom TLS dialer reports its own handshake lifecycle.
		// 自定义 TLS 拨号由此上报握手生命周期。
		trace := httptrace.ContextClientTrace(ctx)
		if trace != nil && trace.TLSHandshakeStart != nil {
			trace.TLSHandshakeStart()
		}
		err = conn.HandshakeContext(ctx)
		if trace != nil && trace.TLSHandshakeDone != nil {
			trace.TLSHandshakeDone(conn.ConnectionState(), err)
		}
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return &guardedGenerationConn{Conn: conn, check: check}, nil
	}
	request.Close = true
	return transport.RoundTrip(request)
}
