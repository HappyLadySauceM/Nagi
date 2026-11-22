package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/HappyLadySauceM/Nagi/internal/memory/contracts"
)

type consentRepository interface {
	CreateConsent(context.Context, contracts.CreateConsent) (contracts.ConsentProposal, error)
	Consent(context.Context, int64, string) (contracts.ConsentProposal, error)
	RevokeConsent(context.Context, int64, string, int64, int64) error
}

func (s *Service) CreateConsent(ctx context.Context, in contracts.CreateConsent) (contracts.ConsentProposal, error) {
	repo, ok := s.repo.(consentRepository)
	if !ok {
		return contracts.ConsentProposal{}, fmt.Errorf("memory: consent repository unavailable")
	}
	proposal, err := repo.CreateConsent(ctx, in)
	if err != nil {
		return proposal, err
	}
	return repo.Consent(ctx, in.BotID, proposal.ID)
}

func (s *Service) Consent(ctx context.Context, bot int64, id string) (contracts.ConsentProposal, error) {
	key, err := uuid.Parse(id)
	if bot <= 0 || err != nil {
		return contracts.ConsentProposal{}, contracts.ErrConsentInvalid
	}
	repo, ok := s.repo.(consentRepository)
	if !ok {
		return contracts.ConsentProposal{}, fmt.Errorf("memory: consent repository unavailable")
	}
	return repo.Consent(ctx, bot, key.String())
}

func (s *Service) RevokeConsent(ctx context.Context, bot int64, id string, expected, target int64) (contracts.ConsentProposal, error) {
	key, err := uuid.Parse(id)
	if bot <= 0 || err != nil || expected <= 0 || target < 0 {
		return contracts.ConsentProposal{}, contracts.ErrConsentInvalid
	}
	repo, ok := s.repo.(consentRepository)
	if !ok {
		return contracts.ConsentProposal{}, fmt.Errorf("memory: consent repository unavailable")
	}
	if err := repo.RevokeConsent(ctx, bot, key.String(), expected, target); err != nil {
		return contracts.ConsentProposal{}, err
	}
	return repo.Consent(ctx, bot, key.String())
}
