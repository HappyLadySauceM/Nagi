package application

import (
	"context"
	"fmt"
	"math"
	"time"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
	"github.com/HappyLadySauceM/Nagi/pkg/config"
)

func ExtractionFromConfig(cfg config.Extraction) settingsdomain.Extraction {
	delays := make([]int64, 0, len(cfg.RetryDelays))
	for _, delay := range cfg.RetryDelays {
		delays = append(delays, delay.D().Milliseconds())
	}
	return settingsdomain.Extraction{
		Enabled: cfg.Enabled, Concurrency: cfg.Concurrency, BatchMaxMessages: cfg.BatchMaxMessages,
		BatchMaxRunes: cfg.BatchMaxRunes, MaxWaitMS: cfg.MaxWait.D().Milliseconds(),
		PollIntervalMS: cfg.PollInterval.D().Milliseconds(), LeaseDurationMS: cfg.LeaseDuration.D().Milliseconds(),
		MaxAttempts: cfg.MaxAttempts, RetryDelaysMS: delays,
	}
}

func EffectiveExtraction(settings settingsdomain.Settings, fallback config.Extraction) config.Extraction {
	if settings.Extraction == nil {
		return fallback
	}
	return extractionConfig(*settings.Extraction)
}

func extractionConfig(settings settingsdomain.Extraction) config.Extraction {
	duration := func(ms int64) config.Duration { return config.Duration(time.Duration(ms) * time.Millisecond) }
	delays := make([]config.Duration, 0, len(settings.RetryDelaysMS))
	for _, delay := range settings.RetryDelaysMS {
		delays = append(delays, duration(delay))
	}
	return config.Extraction{
		Enabled: settings.Enabled, Concurrency: settings.Concurrency, BatchMaxMessages: settings.BatchMaxMessages,
		BatchMaxRunes: settings.BatchMaxRunes, MaxWait: duration(settings.MaxWaitMS),
		PollInterval: duration(settings.PollIntervalMS), LeaseDuration: duration(settings.LeaseDurationMS),
		MaxAttempts: settings.MaxAttempts, RetryDelays: delays,
	}
}

func (s *Service) UpdateExtraction(ctx context.Context, extraction settingsdomain.Extraction, expected int64, actor string) (View, error) {
	if err := validateExtraction(extraction); err != nil {
		return View{}, &settingsdomain.ValidationError{Fields: map[string]string{"Extraction": err.Error()}}
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	current := s.provider.Current()
	if current.Version != expected {
		return View{}, ErrConflict
	}
	next := current.Settings
	next.Extraction = &extraction
	return s.updateLocked(ctx, next, current.Settings, expected, actor)
}

func validateExtraction(settings settingsdomain.Extraction) error {
	durations := []int64{settings.MaxWaitMS, settings.PollIntervalMS, settings.LeaseDurationMS}
	durations = append(durations, settings.RetryDelaysMS...)
	for _, duration := range durations {
		if duration < 0 || duration > math.MaxInt64/int64(time.Millisecond) {
			return fmt.Errorf("抽取时间参数超出范围")
		}
	}
	return extractionConfig(settings).Validate()
}
