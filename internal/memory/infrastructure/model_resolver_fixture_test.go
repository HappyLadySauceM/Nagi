package infrastructure

import (
	"context"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
)

type staticModelResolver struct {
	profile models.Profile
	apiKey  string
}

func (r staticModelResolver) ResolveModel(context.Context, string, int64, string, int64) (models.Resolved, error) {
	return models.Resolved{Profile: r.profile, APIKey: r.apiKey}, nil
}

func (r staticModelResolver) BilibiliCredential(context.Context) (string, error) { return "", nil }
