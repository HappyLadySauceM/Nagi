package contracts

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HappyLadySauceM/Nagi/internal/audit/domain"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
)

const modelMediaFinishTimeout = 2 * time.Second

// ModelMediaStartFailureCode identifies a failed start write before dispatch.
// ModelMediaStartFailureCode 标识派发前的开始记录写入失败。
const ModelMediaStartFailureCode = "model_media_audit_start_failed"

// ErrModelMediaStart stops dispatch and retries after a required start write fails.
// ErrModelMediaStart 在必要的开始记录写入失败后阻断派发与重试。
var ErrModelMediaStart = errors.New(ModelMediaStartFailureCode)

type modelMediaKey struct{}
type modelMediaInput struct {
	stage string
	shape media.ModelMediaAttempt
}

// WithModelMedia associates safe media metadata with a concrete model request.
// WithModelMedia 将媒体白名单元数据关联到具体模型请求。
func WithModelMedia(ctx context.Context, stage string, shape media.ModelMediaAttempt) context.Context {
	return context.WithValue(ctx, modelMediaKey{}, modelMediaInput{stage, shape})
}

// ModelMediaTransport joins adapter evidence with complete HTTP write evidence.
// ModelMediaTransport 将适配器输入形态与 HTTP 完整写出证据关联。
type ModelMediaTransport struct {
	Base http.RoundTripper
	// BeforeDispatch receives a private request immediately before the native transport.
	// BeforeDispatch 在调用原生传输之前接收请求的私有副本。
	BeforeDispatch func(*http.Request) error
}

func (t ModelMediaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	dispatched := false
	dispatch := func(request *http.Request) (*http.Response, error) {
		if t.BeforeDispatch != nil {
			request = request.Clone(request.Context())
			if err := t.BeforeDispatch(request); err != nil {
				if request.Body != nil {
					_ = request.Body.Close()
				}
				return nil, err
			}
		}
		dispatched = true
		return base.RoundTrip(request)
	}
	input, ok := req.Context().Value(modelMediaKey{}).(modelMediaInput)
	if !ok {
		return dispatch(req)
	}
	ctx := req.Context()
	run, _ := ctx.Value(runContextKey{}).(*RunContext)
	var event *domain.AIEvent
	if run != nil {
		// Fence later SDK attempts and parallel intent questions after a start failure.
		// 开始记录写入失败后，阻断后续 SDK 尝试和并行意图问题。
		run.mediaStartMu.Lock()
		if !run.mediaStartFailed {
			var err error
			event, err = StartEvent(ctx, input.stage, map[string]int{"Version": media.ModelMediaVersion})
			run.mediaStartFailed = err != nil
		}
		failed := run.mediaStartFailed
		run.mediaStartMu.Unlock()
		if failed {
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, ErrModelMediaStart
		}
	}
	if event == nil {
		return dispatch(req)
	}
	cloned := req.Clone(ctx)
	tracker := &mediaWriteTrace{length: req.ContentLength}
	if req.Body != nil && req.Body != http.NoBody {
		cloned.Body = tracker.observeBody(req.Body)
		if req.GetBody != nil {
			cloned.GetBody = func() (io.ReadCloser, error) {
				body, err := req.GetBody()
				if err != nil {
					return nil, err
				}
				return tracker.observeBody(body), nil
			}
		}
	}
	_, standard := base.(*http.Transport)
	response, callErr := dispatch(cloned.WithContext(httptrace.WithClientTrace(ctx, tracker.trace())))
	// Keep header evidence independently of the stream and its subsequent cleanup.
	// 独立保留响应头证据，不依赖流及后续清理。
	shutdown, _ := ctx.Value(mediaRepairContextKey{}).(context.Context)
	var headers *http.Response
	if response != nil {
		headers = &http.Response{StatusCode: response.StatusCode, ProtoMajor: response.ProtoMajor, Close: response.Close}
	}
	freeze := func(bodyState string) mediaSettlement {
		report := tracker.report(input.shape, standard, headers, callErr, bodyState)
		if !dispatched {
			report.Reason, report.AttemptCountUnknown = media.MediaFailedBeforeWrite, false
		}
		report = media.SafeMediaCall(report)
		payload, _ := json.Marshal(report)
		return mediaSettlement{recorder: run.recorder, runID: run.runID, eventID: event.ID, status: report.Outcome,
			code: report.Reason, output: payload, shutdown: shutdown}
	}
	settle := func(terminal mediaSettlement) {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelMediaFinishTimeout)
		defer cancel()
		terminal.finish(finishCtx)
	}
	switch {
	case response == nil:
		settle(freeze(""))
	case response.Body == http.NoBody:
		settle(freeze(media.MediaBodyComplete))
	case response.Body == nil:
		settle(freeze(media.MediaUnknown))
	default:
		length := int64(-1)
		if standard && response.ProtoMajor == 1 && !response.Uncompressed {
			length = response.ContentLength
		}
		run.mediaMu.Lock()
		if run.mediaEnded {
			run.mediaMu.Unlock()
			settle(freeze(media.MediaUnknown))
			break
		}
		if run.mediaPending == nil {
			run.mediaPending = make(map[string]func(string) mediaSettlement)
		}
		run.mediaPending[event.ID] = freeze
		run.mediaMu.Unlock()
		response.Body = &mediaResponseBody{ReadCloser: response.Body, length: length, finish: func(state string) {
			run.mediaMu.Lock()
			selectEvidence := run.mediaPending[event.ID]
			if selectEvidence == nil {
				run.mediaMu.Unlock()
				return
			}
			terminal := selectEvidence(state)
			delete(run.mediaPending, event.ID)
			run.mediaMu.Unlock()
			settle(terminal)
		}}
	}
	return response, callErr
}

// FinishPendingModelMedia freezes unobserved bodies at the owning run's cutoff.
// FinishPendingModelMedia 在所属运行收尾时冻结尚未观察到结束的响应体。
func FinishPendingModelMedia(ctx context.Context) {
	run, _ := ctx.Value(runContextKey{}).(*RunContext)
	if run == nil {
		return
	}
	run.mediaMu.Lock()
	terminals := make([]mediaSettlement, 0, len(run.mediaPending))
	for _, freeze := range run.mediaPending {
		terminals = append(terminals, freeze(media.MediaUnknown))
	}
	run.mediaPending = nil
	run.mediaEnded = true
	run.mediaMu.Unlock()
	if len(terminals) == 0 {
		return
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelMediaFinishTimeout)
	defer cancel()
	for _, terminal := range terminals {
		terminal.finish(finishCtx)
	}
}

type mediaRepairContextKey struct{}

// WithMediaRepairContext binds background settlement to the worker's shutdown.
// WithMediaRepairContext 让后台终态补写随工作器停机取消。
func WithMediaRepairContext(ctx, shutdown context.Context) context.Context {
	return context.WithValue(ctx, mediaRepairContextKey{}, shutdown)
}

type mediaSettlement struct {
	recorder                     AIRecorder
	runID, eventID, status, code string
	output                       []byte
	shutdown                     context.Context
}

var (
	mediaRepairPending = make(chan struct{}, 64)
	mediaRepairWriters = make(chan struct{}, 2)
)

const mediaRepairLifetime = 30 * time.Second

func (s mediaSettlement) finish(ctx context.Context) {
	deadline := time.Now().Add(mediaRepairLifetime)
	err := s.recorder.FinishEvent(ctx, s.eventID, s.status, s.code, s.output)
	if err == nil || s.permanentError(err) {
		return
	}
	select {
	case mediaRepairPending <- struct{}{}:
		shutdown := s.shutdown
		if shutdown == nil {
			shutdown = context.Background()
		}
		go s.repair(shutdown, deadline)
	default:
		s.reportFailure("capacity")
	}
}

func (s mediaSettlement) repair(shutdown context.Context, deadline time.Time) {
	defer func() { <-mediaRepairPending }()
	ctx, cancel := context.WithDeadline(shutdown, deadline)
	defer cancel()
	for _, delay := range []time.Duration{250 * time.Millisecond, 3 * time.Second} {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.reportFailure("expired_or_stopped")
			return
		case <-timer.C:
		}
		select {
		case <-ctx.Done():
			s.reportFailure("expired_or_stopped")
			return
		case mediaRepairWriters <- struct{}{}:
		}
		if ctx.Err() != nil {
			<-mediaRepairWriters
			s.reportFailure("expired_or_stopped")
			return
		}
		writeCtx, stopWrite := context.WithTimeout(ctx, modelMediaFinishTimeout)
		err := s.recorder.FinishEvent(writeCtx, s.eventID, s.status, s.code, s.output)
		stopWrite()
		<-mediaRepairWriters
		if err == nil || s.permanentError(err) {
			return
		}
	}
	s.reportFailure("retry_exhausted")
}

func (s mediaSettlement) permanentError(err error) bool {
	if errors.Is(err, domain.ErrConflict) {
		s.reportFailure("terminal_conflict")
		return true
	}
	if errors.Is(err, domain.ErrNotFound) {
		s.reportFailure("event_missing")
		return true
	}
	return false
}

func (s mediaSettlement) reportFailure(reason string) {
	slog.Warn("model media audit settlement incomplete", "run_id", s.runID, "event_id", s.eventID, "reason", reason)
}

type mediaWriteTrace struct {
	mu          sync.Mutex
	length      int64
	body        *mediaRequestBody
	current     *mediaWriteAttempt
	h2          bool
	attempts    []*mediaWriteAttempt
	pending     int
	connections int
	acquiring   bool
	ambiguous   bool
}

type mediaWriteAttempt struct {
	body         *mediaRequestBody
	initialBytes int64
	waited       bool
	h2           bool
	complete     bool
}

func (t *mediaWriteTrace) observeBody(body io.ReadCloser) *mediaRequestBody {
	t.mu.Lock()
	defer t.mu.Unlock()
	watched := &mediaRequestBody{ReadCloser: body}
	t.body = watched
	return watched
}

func (t *mediaWriteTrace) nextConnection() {
	if t.pending != 0 || t.connections > len(t.attempts) {
		t.ambiguous = true
	}
	t.current = nil
	t.h2 = false
}

func (t *mediaWriteTrace) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(string) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.acquiring && t.connections != 0 {
				t.ambiguous = true
			}
			t.acquiring = true
			t.nextConnection()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.acquiring {
				t.nextConnection()
			}
			t.connections++
			t.acquiring = false
			conn, ok := info.Conn.(*tls.Conn)
			t.h2 = ok && conn.ConnectionState().NegotiatedProtocol == "h2"
		},
		WroteHeaders: func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			attempt := &mediaWriteAttempt{body: t.body, h2: t.h2}
			if attempt.body != nil {
				attempt.initialBytes = attempt.body.bytes.Load()
			}
			t.current = attempt
			t.attempts = append(t.attempts, attempt)
			t.pending++
			if t.pending > 1 {
				t.ambiguous = true
			}
		},
		Wait100Continue: func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.current != nil && !t.ambiguous {
				t.current.waited = true
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.current == nil || t.pending != 1 || t.ambiguous {
				if info.Err == nil || t.pending != 0 {
					t.ambiguous = true
				}
				return
			}
			t.pending--
			if info.Err != nil {
				return
			}
			attempt := t.current
			if !attempt.h2 || attempt.body == nil {
				return
			}
			bytesRead := attempt.body.bytes.Load() - attempt.initialBytes
			// Native HTTP/2 completes this callback after the END_STREAM flush.
			// 原生 HTTP/2 在 END_STREAM 刷新后完成该回调。
			attempt.complete = attempt.body.eof.Load() && bytesRead > 0 && (t.length < 0 || bytesRead == t.length)
		},
	}
}

func (t *mediaWriteTrace) report(shape media.ModelMediaAttempt, standard bool, resp *http.Response, callErr error, bodyState string) media.ModelMediaCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	report := media.ModelMediaCall{
		Outcome:             media.MediaSucceeded,
		AttemptCountUnknown: !standard || t.connections != len(t.attempts) || t.current == nil || t.ambiguous || callErr != nil || resp == nil,
	}
	if callErr != nil || resp == nil || resp.StatusCode >= 300 || bodyState == media.MediaBodyReadFailed {
		report.Outcome = media.MediaFailed
	}
	switch {
	case standard && t.acquiring && t.connections == 0 && len(t.attempts) == 0 && callErr != nil:
		report.Reason = media.MediaFailedBeforeWrite
	case callErr != nil || resp == nil:
		report.Reason = media.MediaUnattributed
	case bodyState == media.MediaBodyReadFailed:
		report.Reason = media.MediaBodyReadFailed
	}
	// Header callbacks are independent evidence that request writing began.
	// 请求头回调是该笔请求已开始写入的独立证据。
	for _, attempt := range t.attempts {
		row := media.ModelMediaAttempt{Shape: media.MediaUnknown, State: media.MediaUnknown}
		if standard && attempt.complete {
			row = shape
			row.State = media.MediaWritten
		}
		if resp != nil && attempt == t.current && !t.ambiguous {
			row.HTTPStatus = resp.StatusCode
			row.ResponseBody = bodyState
			if standard && resp.ProtoMajor == 1 && attempt.waited && resp.Close && attempt.body != nil && attempt.body.closed.Load() && attempt.body.bytes.Load() == attempt.initialBytes {
				// HTTP/1 flushes headers before waiting for 100 Continue.
				// HTTP/1 在等待 100 Continue 之前已刷新请求头。
				row.State = media.MediaPartialWrite
			}
		}
		report.Attempts = append(report.Attempts, row)
	}
	if resp != nil && (t.current == nil || t.ambiguous) {
		report.HTTPStatus = resp.StatusCode
		report.ResponseBody = bodyState
	}
	return report
}

type mediaRequestBody struct {
	io.ReadCloser
	bytes  atomic.Int64
	eof    atomic.Bool
	closed atomic.Bool
}

func (b *mediaRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes.Add(int64(n))
	if err == io.EOF {
		b.eof.Store(true)
	}
	return n, err
}

func (b *mediaRequestBody) Close() error {
	err := b.ReadCloser.Close()
	b.closed.Store(true)
	return err
}

type mediaResponseBody struct {
	io.ReadCloser
	length int64
	read   atomic.Int64
	finish func(string)
}

func (b *mediaResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read.Add(int64(n))
	switch {
	case errors.Is(err, io.EOF):
		b.finish(media.MediaBodyComplete)
	case err != nil:
		b.finish(media.MediaBodyReadFailed)
	}
	return n, err
}

func (b *mediaResponseBody) Close() error {
	err := b.ReadCloser.Close()
	switch {
	case err != nil:
		b.finish(media.MediaUnknown)
	case b.length >= 0 && b.read.Load() == b.length:
		b.finish(media.MediaBodyComplete)
	case b.length >= 0 && b.read.Load() < b.length:
		b.finish(media.MediaBodyClosedEarly)
	default:
		b.finish(media.MediaBodyClosedUnknown)
	}
	return err
}
