package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAPIEnvelopeShape verifies the admin API JSON envelope for success and failure paths.
// TestAPIEnvelopeShape 验证管理 API 成功与失败路径的 JSON 信封形态。
func TestAPIEnvelopeShape(t *testing.T) {
	tests := []struct {
		name       string
		write      func(http.ResponseWriter)
		wantStatus int
		wantCode   int
		wantMsg    string
		wantData   bool
	}{
		{
			name: "success",
			write: func(w http.ResponseWriter) {
				WriteSuccess(w, map[string]int{"n": 1})
			},
			wantStatus: http.StatusOK,
			wantCode:   http.StatusOK,
			wantMsg:    "success",
			wantData:   true,
		},
		{
			name: "failure",
			write: func(w http.ResponseWriter) {
				WriteFailure(w, http.StatusBadRequest, "bad input")
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   http.StatusBadRequest,
			wantMsg:    "bad input",
		},
		{
			name: "unauthorized",
			write: func(w http.ResponseWriter) {
				WriteFailure(w, http.StatusUnauthorized, "unauthorized")
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   http.StatusUnauthorized,
			wantMsg:    "unauthorized",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.write(rec)
			if rec.Code != tc.wantStatus {
				t.Fatalf("http status=%d want=%d", rec.Code, tc.wantStatus)
			}
			var env Envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.Code != tc.wantCode || env.Message != tc.wantMsg {
				t.Fatalf("envelope=%+v", env)
			}
			if tc.wantData && env.Data == nil {
				t.Fatal("expected data field")
			}
			if !tc.wantData && env.Data != nil {
				t.Fatalf("unexpected data: %v", env.Data)
			}
		})
	}
}
