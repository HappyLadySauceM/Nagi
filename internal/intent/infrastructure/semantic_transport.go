package infrastructure

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type RequestTransport struct {
	base      *http.Transport
	Dial      func(context.Context, string, string) (net.Conn, error)
	TLSConfig *tls.Config
}

func NewRequestTransport() RequestTransport {
	return RequestTransport{base: &http.Transport{}, Dial: (&net.Dialer{}).DialContext}
}

type requestGuardKey struct{}

type RequestGuard interface {
	Write(context.Context, string, func() error) (string, error)
	Complete(bool, bool, bool, string)
}

func WithRequestGuard(ctx context.Context, guard RequestGuard) context.Context {
	return context.WithValue(ctx, requestGuardKey{}, guard)
}

type firstIntentWrite struct {
	net.Conn
	writeFirst     func(func() error) error
	wrote, partial *atomic.Bool
	pending        []byte
	started        bool
}

func (c *firstIntentWrite) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.started {
		n, err := c.Conn.Write(p)
		if n < len(p) {
			c.partial.Store(true)
		}
		return n, err
	}
	prior := len(c.pending)
	c.pending = append(c.pending, p...)
	boundary := bytes.Index(c.pending, []byte("\r\n\r\n"))
	if boundary < 0 || boundary+4 == len(c.pending) {
		return len(p), nil
	}
	payload := c.pending
	c.pending = nil
	c.started = true
	var n int
	err := c.writeFirst(func() error {
		var err error
		n, err = c.Conn.Write(payload)
		if n > 0 {
			c.wrote.Store(true)
			c.partial.Store(n < len(payload))
		}
		return err
	})
	return max(0, n-prior), err
}

func (t RequestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	guard, _ := req.Context().Value(requestGuardKey{}).(RequestGuard)
	if guard == nil {
		return t.base.RoundTrip(req)
	}
	var wrote, partial atomic.Bool
	var lifecycle sync.Mutex
	var finished, enteredWrite bool
	var denial string
	wrap := func(conn net.Conn) net.Conn {
		writeFirst := func(write func() error) error {
			lifecycle.Lock()
			defer lifecycle.Unlock()
			if finished {
				return context.Canceled
			}
			ctx, cancel := context.WithTimeout(req.Context(), 250*time.Millisecond)
			defer cancel()
			reason, err := guard.Write(ctx, req.URL.String(), func() error {
				deadline, _ := ctx.Deadline()
				if err := conn.SetWriteDeadline(deadline); err != nil {
					return err
				}
				defer conn.SetWriteDeadline(time.Time{})
				if err := ctx.Err(); err != nil {
					return err
				}
				enteredWrite = true
				return write()
			})
			if reason != "" {
				denial = reason
				err = errors.New("intent: request background no longer authorized")
			}
			return err
		}
		return &firstIntentWrite{Conn: conn, writeFirst: writeFirst, wrote: &wrote, partial: &partial}
	}
	transport := &http.Transport{DisableKeepAlives: true}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := t.Dial(ctx, network, addr)
		if err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			return nil, err
		}
		return wrap(conn), nil
	}
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if t.TLSConfig != nil {
			config = t.TLSConfig.Clone()
		}
		config.NextProtos = []string{"http/1.1"}
		if config.ServerName == "" {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			config.ServerName = host
		}
		raw, err := t.Dial(ctx, network, addr)
		if err != nil {
			if raw != nil {
				_ = raw.Close()
			}
			return nil, err
		}
		conn := tls.Client(raw, config)
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return wrap(conn), nil
	}
	outbound := req.Clone(req.Context())
	outbound.GetBody = nil
	outbound.Close = true
	response, err := transport.RoundTrip(outbound)
	lifecycle.Lock()
	defer lifecycle.Unlock()
	finished = true
	confirmed := wrote.Load() || response != nil
	guard.Complete(confirmed, partial.Load(), enteredWrite && !confirmed, denial)
	return response, err
}
