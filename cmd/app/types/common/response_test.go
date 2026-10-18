package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteSuccessUsesEnvelopeWithHTTPStatus(t *testing.T) {
	// 验证成功信封 code 与 HTTP 状态一致并携带数据。
	rec := httptest.NewRecorder()
	WriteSuccess(rec, map[string]string{"k": "v"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != http.StatusOK || got.Message != "success" {
		t.Fatalf("envelope = %+v", got)
	}
}

func TestWriteFailureKeepsCodeAlignedWithStatus(t *testing.T) {
	// 验证失败信封 code 与状态码一致且消息安全。
	rec := httptest.NewRecorder()
	WriteFailure(rec, http.StatusServiceUnavailable, "onebot connection not ready")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	var got Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != http.StatusServiceUnavailable || got.Message != "onebot connection not ready" {
		t.Fatalf("envelope = %+v", got)
	}
}
