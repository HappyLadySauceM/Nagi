package contracts

import (
	"context"
	"time"
)

type IntentFact struct {
	ID              string
	Content         string
	SourceMessageID int64
	UpdatedAt       time.Time
}

type IntentFactReader interface {
	IntentFacts(context.Context, int64, int64, string, string, string, []string, []string) ([]IntentFact, error)
}
