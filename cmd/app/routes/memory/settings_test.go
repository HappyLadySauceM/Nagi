package memory

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauceM/Nagi/cmd/app/svc"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

func TestGetExtractionSettingsReturnsGlobalReadOnlyConfig(t *testing.T) {
	handler := NewHandler(&svc.ServiceContext{Config: &config.Config{Memory: config.Memory{Extraction: config.Extraction{
		Enabled: true, Concurrency: 2, BatchMaxMessages: 7, BatchMaxRunes: 2500,
		MaxWait: config.Duration(3 * time.Second), PollInterval: config.Duration(500 * time.Millisecond),
		LeaseDuration: config.Duration(20 * time.Second), MaxAttempts: 4,
		RetryDelays: []config.Duration{config.Duration(10 * time.Second), config.Duration(30 * time.Second)},
	}}}})
	rec := httptest.NewRecorder()
	handler.GetExtractionSettings(rec, httptest.NewRequest(http.MethodGet, "/api/v1/memory-extraction/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"Scope":"global"`, `"Enabled":true`, `"Concurrency":2`, `"BatchMaxMessages":7`, `"BatchMaxRunes":2500`, `"MaxWaitMS":3000`, `"PollIntervalMS":500`, `"LeaseDurationMS":20000`, `"MaxAttempts":4`, `"Source":"config"`, `"Editable":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings body missing %s: %s", want, body)
		}
	}
}
