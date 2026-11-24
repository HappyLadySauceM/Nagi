package assembly

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
)

type intentTransport struct {
	base      *http.Transport
	dial      func(context.Context, string, string) (net.Conn, error)
	tlsConfig *tls.Config
}

func newIntentTransport() intentTransport {
	return intentTransport{base: &http.Transport{}, dial: (&net.Dialer{}).DialContext}
}

type firstIntentWrite struct {
	net.Conn
	allow          func() error
	first          sync.Once
	denied         error
	wrote, partial *atomic.Bool
}

func (c *firstIntentWrite) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.first.Do(func() { c.denied = c.allow() })
	if c.denied != nil {
		return 0, c.denied
	}
	n, err := c.Conn.Write(p)
	if n > 0 {
		if n < len(p) {
			c.partial.Store(true)
		}
		c.wrote.Store(true)
	}
	return n, err
}

func (t intentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	guard, _ := req.Context().Value(intentRequestGuardKey{}).(*intentRequestGuard)
	if guard == nil {
		return t.base.RoundTrip(req)
	}
	var wrote, partial, blocked, failedDial, enteredWrite atomic.Bool
	allow := func() error {
		if err := req.Context().Err(); err != nil {
			blocked.Store(true)
			return err
		}
		if guard.failure.Load() == nil {
			if reason := guard.check(req.Context(), req.URL.String()); reason != "" {
				guard.failure.Store(&reason)
			}
		}
		if guard.failure.Load() != nil {
			blocked.Store(true)
			return errIntentBackgroundDenied
		}
		if err := req.Context().Err(); err != nil {
			blocked.Store(true)
			return err
		}
		enteredWrite.Store(true)
		return nil
	}
	wrap := func(conn net.Conn) net.Conn {
		return &firstIntentWrite{Conn: conn, allow: allow, wrote: &wrote, partial: &partial}
	}
	transport := &http.Transport{DisableKeepAlives: true}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := t.dial(ctx, network, addr)
		if err != nil {
			if conn == nil {
				failedDial.Store(true)
			} else {
				_ = conn.Close()
			}
			return nil, err
		}
		return wrap(conn), nil
	}
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if t.tlsConfig != nil {
			config = t.tlsConfig.Clone()
		}
		config.NextProtos = []string{"http/1.1"}
		if config.ServerName == "" {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				failedDial.Store(true)
				return nil, err
			}
			config.ServerName = host
		}
		raw, err := t.dial(ctx, network, addr)
		if err != nil {
			if raw == nil {
				failedDial.Store(true)
			} else {
				_ = raw.Close()
			}
			return nil, err
		}
		conn := tls.Client(raw, config)
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			failedDial.Store(true)
			return nil, err
		}
		return wrap(conn), nil
	}
	outbound := req.Clone(req.Context())
	outbound.GetBody = nil
	outbound.Close = true
	response, err := transport.RoundTrip(outbound)
	if wrote.Load() || response != nil {
		guard.sent.Add(1)
		guard.partial.Store(partial.Load())
	} else if !blocked.Load() && !failedDial.Load() {
		guard.unknown.Add(1)
		if enteredWrite.Load() && guard.failure.Load() == nil {
			reason := "semantic_dispatch_unknown"
			guard.failure.Store(&reason)
		}
	}
	return response, err
}
