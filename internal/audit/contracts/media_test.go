package contracts

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
)

func TestModelMediaTransportReportsActualWriteBoundary(t *testing.T) {
	const payload = "fixture-payload-never-in-audit"
	var fullWrites atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reject":
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusExpectationFailed)
			_, _ = io.WriteString(w, "fixture rejected")
		case "/truncated":
			if got, err := io.ReadAll(r.Body); string(got) != payload || err != nil {
				t.Error("incomplete fixture request")
			}
			fullWrites.Add(1)
			w.Header().Set("Content-Length", "20")
			_, _ = io.WriteString(w, "short")
		case "/ok":
			if got, err := io.ReadAll(r.Body); string(got) != payload || err != nil {
				t.Error("incomplete fixture request")
			}
			fullWrites.Add(1)
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer server.Close()
	for _, c := range []struct {
		path, expect, state, outcome, reason string
		status                               int
		readFailed                           bool
	}{
		{"/reject", "100-continue", media.MediaPartialWrite, media.MediaFailed, "", http.StatusExpectationFailed, false},
		{"/ok", "100-continue", media.MediaUnknown, media.MediaSucceeded, "", http.StatusOK, false},
		{"/truncated", "", media.MediaUnknown, media.MediaFailed, media.MediaBodyReadFailed, http.StatusOK, true},
	} {
		t.Run(c.path, func(t *testing.T) {
			recorder := &runRecorder{}
			ctx := WithRun(context.Background(), recorder, "run-fixture")
			ctx = WithModelMedia(ctx, "generation.media_request", media.ModelMediaAttempt{Shape: media.MediaImage, Images: []int{2}})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+c.path, strings.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			if c.path == "/reject" {
				written := make(chan struct{})
				trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { close(written) }}
				req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
				req.Header.Set("Expect", c.expect)
				res, err := (ModelMediaTransport{}).RoundTrip(req)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-written:
				case <-time.After(time.Second):
					t.Fatal("request writer did not finish")
				}
				assertMediaBoundary(t, recorder, res, payload, c.state, c.outcome, c.reason, c.status, c.readFailed)
				return
			}
			if c.expect != "" {
				req.Header.Set("Expect", c.expect)
			}
			res, err := (ModelMediaTransport{}).RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			assertMediaBoundary(t, recorder, res, payload, c.state, c.outcome, c.reason, c.status, c.readFailed)
		})
	}
	if fullWrites.Load() != 2 {
		t.Fatalf("full writes=%d", fullWrites.Load())
	}
}

func assertMediaBoundary(t *testing.T, recorder *runRecorder, res *http.Response, payload, state, outcome, reason string, status int, readFailed bool) {
	t.Helper()
	_, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readFailed != errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("response read err=%v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatal("missing audit event")
	}
	event := recorder.events[0]
	if strings.Contains(string(event.Input)+string(event.Output), payload) {
		t.Fatal("payload leaked")
	}
	var report media.ModelMediaCall
	if err := json.Unmarshal(event.Output, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Attempts) != 1 || report.AttemptCountUnknown || report.Outcome != outcome || report.Reason != reason {
		t.Fatalf("report=%s", event.Output)
	}
	attempt := report.Attempts[0]
	wantResponse := media.MediaBodyComplete
	if readFailed {
		wantResponse = media.MediaBodyReadFailed
	}
	if attempt.State != state || attempt.HTTPStatus != status || attempt.ResponseBody != wantResponse {
		t.Fatalf("attempt=%+v", attempt)
	}
	if state == media.MediaWritten && (attempt.Shape != media.MediaImage || !reflect.DeepEqual(attempt.Images, []int{2})) || state != media.MediaWritten && (attempt.Shape != media.MediaUnknown || len(attempt.Images) != 0) {
		t.Fatalf("attempt=%+v", attempt)
	}
	t.Logf("state=%s status=%d read_failed=%t call=%s attempts=%d", attempt.State, attempt.HTTPStatus, readFailed, report.Outcome, len(report.Attempts))
}

func TestModelMediaRetryEvidenceRequiresIndependentRequestStart(t *testing.T) {
	for _, secondStarted := range []bool{false, true} {
		t.Run(fmt.Sprint(secondStarted), func(t *testing.T) {
			tracker := &mediaWriteTrace{length: 1}
			body := tracker.observeBody(io.NopCloser(strings.NewReader("x")))
			trace := tracker.trace()
			trace.GetConn("fixture")
			trace.GotConn(httptrace.GotConnInfo{})
			trace.WroteHeaders()
			_, _ = io.ReadAll(body)
			trace.WroteRequest(httptrace.WroteRequestInfo{})
			trace.GetConn("fixture")
			trace.GotConn(httptrace.GotConnInfo{})
			if secondStarted {
				trace.WroteHeaders()
			}
			result := tracker.report(media.ModelMediaAttempt{Shape: media.MediaImage, Images: []int{2}}, true, nil, errors.New("fixture final error"), "")
			want := 1
			if secondStarted {
				want++
			}
			if result.Outcome != media.MediaFailed || result.Reason != media.MediaUnattributed || !result.AttemptCountUnknown || len(result.Attempts) != want || result.Attempts[0].State != media.MediaUnknown || len(result.Attempts[0].Images) != 0 {
				t.Fatalf("report=%+v", result)
			}
			if secondStarted && result.Attempts[1].State != media.MediaUnknown {
				t.Fatalf("report=%+v", result)
			}
			t.Logf("second_started=%t confirmed_records=%d total_unknown=%t reason=%s", secondStarted, want, result.AttemptCountUnknown, result.Reason)
		})
	}
}

type fixtureWriteFailure struct {
	net.Conn
	writes    *atomic.Int32
	passFirst bool
}

func (c fixtureWriteFailure) Write(p []byte) (int, error) {
	if c.writes.Add(1) == 1 && c.passFirst {
		return c.Conn.Write(p)
	}
	return 0, io.ErrClosedPipe
}

func TestModelMediaHTTP1FinalFlushFailureIsUnknown(t *testing.T) {
	for _, observe := range []bool{false, true} {
		t.Run(fmt.Sprint(observe), func(t *testing.T) {
			var received, writes, callback atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.ReadAll(r.Body); err == nil {
					received.Add(1)
				}
			}))
			defer server.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return fixtureWriteFailure{Conn: conn, writes: &writes, passFirst: observe}, nil
			}}
			defer transport.CloseIdleConnections()
			recorder := &runRecorder{}
			ctx := WithRun(context.Background(), recorder, "fixture")
			ctx = WithModelMedia(ctx, "generation.media_request", media.ModelMediaAttempt{Shape: media.MediaImage, Images: []int{2}})
			ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					callback.Add(1)
				}
			}})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("short buffered fixture"))
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: transport, Timeout: time.Second}
			if observe {
				client.Transport = ModelMediaTransport{Base: transport}
			}
			res, err := client.Do(req)
			if res != nil || !errors.Is(err, io.ErrClosedPipe) || received.Load() != 0 {
				t.Fatalf("result err=%v received=%d", err, received.Load())
			}
			if !observe {
				if callback.Load() != 1 || writes.Load() != 1 {
					t.Fatalf("callback=%d writes=%d", callback.Load(), writes.Load())
				}
				t.Log("native buffered transport: WroteRequest(nil)=1 final flush failed=1 complete bodies received=0")
				return
			}
			if writes.Load() != 2 || len(recorder.events) != 1 {
				t.Fatalf("writes=%d events=%d", writes.Load(), len(recorder.events))
			}
			var report media.ModelMediaCall
			if err := json.Unmarshal(recorder.events[0].Output, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Attempts) != 1 || report.Attempts[0].State != media.MediaUnknown || len(report.Attempts[0].Images) != 0 || report.Outcome != media.MediaFailed {
				t.Fatalf("report=%+v", report)
			}
			t.Log("observed transport: body write failed, complete bodies received=0, audit write=unknown")
		})
	}
}

func TestModelMediaRetainsCompletedHTTP2WriteAfterRedialFailure(t *testing.T) {
	tracker := &mediaWriteTrace{length: 1}
	body := tracker.observeBody(io.NopCloser(strings.NewReader("x")))
	trace := tracker.trace()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	var conn *tls.Conn
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { conn, _ = info.Conn.(*tls.Conn) }})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if conn == nil || conn.ConnectionState().NegotiatedProtocol != "h2" {
		t.Fatal("missing native HTTP/2 connection")
	}
	trace.GetConn("fixture")
	trace.GotConn(httptrace.GotConnInfo{Conn: conn})
	trace.WroteHeaders()
	_, _ = io.ReadAll(body)
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	trace.GetConn("fixture")
	got := tracker.report(media.ModelMediaAttempt{Shape: media.MediaImage, Images: []int{2}}, true, nil, io.ErrClosedPipe, "")
	if got.Outcome != media.MediaFailed || got.Reason != media.MediaUnattributed || !got.AttemptCountUnknown || len(got.Attempts) != 1 || got.Attempts[0].State != media.MediaWritten || got.Attempts[0].Shape != media.MediaImage || !reflect.DeepEqual(got.Attempts[0].Images, []int{2}) || got.Attempts[0].HTTPStatus != 0 {
		t.Fatalf("report=%+v", got)
	}
}

func TestModelMediaEndsUnobservedBodyWithKnownStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	recorder := &runRecorder{}
	ctx, cancel := context.WithTimeout(WithRun(context.Background(), recorder, "fixture"), time.Second)
	defer cancel()
	ctx = WithModelMedia(ctx, "generation.media_request", media.ModelMediaAttempt{Shape: media.MediaTextOnly})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := (ModelMediaTransport{Base: server.Client().Transport}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	FinishPendingModelMedia(ctx)
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	FinishPendingModelMedia(ctx)
	var report media.ModelMediaCall
	if len(recorder.events) != 1 || json.Unmarshal(recorder.events[0].Output, &report) != nil || report.Outcome != media.MediaSucceeded || len(report.Attempts) != 1 || report.Attempts[0].HTTPStatus != http.StatusAccepted || report.Attempts[0].ResponseBody != media.MediaUnknown {
		t.Fatalf("events=%+v report=%+v", recorder.events, report)
	}
}

type timedOutMediaRecorder struct {
	runRecorder
	attempts      chan domain.AIEvent
	firstTimedOut atomic.Bool
	committed     bool
}

func (r *timedOutMediaRecorder) FinishEvent(ctx context.Context, id, status, code string, output []byte) error {
	event := domain.AIEvent{ID: id, Status: status, ErrorCode: code, Output: append([]byte(nil), output...)}
	if r.firstTimedOut.CompareAndSwap(false, true) {
		if r.committed {
			if err := r.runRecorder.FinishEvent(ctx, id, status, code, output); err != nil {
				return err
			}
		}
		r.attempts <- event
		return context.DeadlineExceeded
	}
	err := r.runRecorder.FinishEvent(ctx, id, status, code, output)
	r.attempts <- event
	return err
}

func TestModelMediaRetriesTheFrozenEvent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		terminal  error
		committed bool
		bodyState string
	}{
		{"EOF before cutoff", io.EOF, false, media.MediaBodyComplete},
		{"read error before cutoff", io.ErrUnexpectedEOF, false, media.MediaBodyReadFailed},
		{"cutoff before close", nil, false, media.MediaUnknown},
		{"write committed before timeout", io.EOF, true, media.MediaBodyComplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &timedOutMediaRecorder{attempts: make(chan domain.AIEvent, 3), committed: tc.committed}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			ctx = WithMediaRepairContext(WithRun(ctx, r, "fixture"), ctx)
			ctx = WithModelMedia(ctx, "generation.media_request", media.ModelMediaAttempt{Shape: media.MediaTextOnly})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1", strings.NewReader("fixture"))
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			transport := ModelMediaTransport{Base: retryTestTransport(func(req *http.Request) (*http.Response, error) {
				requests.Add(1)
				_ = req.Body.Close()
				return &http.Response{StatusCode: http.StatusAccepted, Body: terminalTestBody{tc.terminal}}, nil
			})}
			response, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			if tc.terminal != nil {
				_, _ = response.Body.Read(make([]byte, 1))
			}
			FinishPendingModelMedia(ctx)
			_ = response.Body.Close()
			first := <-r.attempts
			select {
			case repaired := <-r.attempts:
				if !reflect.DeepEqual(first, repaired) {
					t.Fatalf("evidence changed on retry: %s -> %s", first.Output, repaired.Output)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing audit repair")
			}
			var report media.ModelMediaCall
			if err := json.Unmarshal(first.Output, &report); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 || len(r.events) != 1 || report.HTTPStatus != http.StatusAccepted || report.ResponseBody != tc.bodyState {
				t.Fatalf("requests=%d events=%d report=%+v", requests.Load(), len(r.events), report)
			}
		})
	}
}

type retryTestTransport func(*http.Request) (*http.Response, error)

func (f retryTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type terminalTestBody struct{ terminal error }

func (b terminalTestBody) Read([]byte) (int, error) { return 0, b.terminal }
func (terminalTestBody) Close() error               { return nil }

func TestModelMediaStartFailureNeverDispatches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fault   error
		commit  bool
		audited bool
	}{
		{name: "explicit", fault: errors.New("fixture raw storage error"), audited: true},
		{name: "uncertain", fault: context.DeadlineExceeded, commit: true, audited: true},
		{name: "unaudited"},
		{name: "normal", commit: true, audited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dispatched int
			base := retryTestTransport(func(req *http.Request) (*http.Response, error) {
				dispatched++
				_ = req.Body.Close()
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
			fault := &mediaStartTestRecorder{fault: tc.fault, commit: tc.commit}
			ctx := context.Background()
			if tc.audited {
				ctx = WithRun(ctx, fault, "fixture-run")
			}
			ctx = WithModelMedia(ctx, "intent.media_request", media.ModelMediaAttempt{Shape: media.MediaTextOnly})
			for i := 0; i < 1+boolCount(tc.fault != nil); i++ {
				body := &mediaStartTestBody{Reader: strings.NewReader("fixture")}
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/model", body)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := (ModelMediaTransport{Base: base}).RoundTrip(req)
				if !body.closed {
					t.Fatal("request body was not closed")
				}
				if tc.fault != nil {
					if resp != nil || err != ErrModelMediaStart || dispatched != 0 || fault.starts != 1 || len(fault.events) != boolCount(tc.commit) || fault.finishes != 0 {
						t.Fatalf("resp=%v err=%v dispatched=%d starts=%d events=%d finishes=%d", resp, err, dispatched, fault.starts, len(fault.events), fault.finishes)
					}
				} else if resp == nil || err != nil || dispatched != 1 || fault.starts != boolCount(tc.audited) || fault.finishes != boolCount(tc.audited) {
					t.Fatalf("resp=%v err=%v dispatched=%d starts=%d finishes=%d", resp, err, dispatched, fault.starts, fault.finishes)
				}
			}
		})
	}
}

func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}

type mediaStartTestBody struct {
	io.Reader
	closed bool
}

func (b *mediaStartTestBody) Close() error { b.closed = true; return nil }

type mediaStartTestRecorder struct {
	runRecorder
	fault    error
	commit   bool
	starts   int
	finishes int
}

func (r *mediaStartTestRecorder) RecordEvent(ctx context.Context, event domain.AIEvent) (domain.AIEvent, error) {
	r.starts++
	if !r.commit {
		return domain.AIEvent{}, r.fault
	}
	stored, err := r.runRecorder.RecordEvent(ctx, event)
	if err != nil {
		return stored, err
	}
	return stored, r.fault
}

func (r *mediaStartTestRecorder) FinishEvent(ctx context.Context, id, status, code string, output []byte) error {
	r.finishes++
	return r.runRecorder.FinishEvent(ctx, id, status, code, output)
}
