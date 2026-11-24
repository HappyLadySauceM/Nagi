package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	settingscontracts "github.com/HappyLadySauceM/Nagi/internal/settings/contracts"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

var ErrIntentMemoryDestination = errors.New("settings: intent memory destination unavailable")

func IntentModelDestination(profile models.Profile) (settingscontracts.IntentDestination, error) {
	endpoint := profile.BaseURL
	if profile.Protocol == "openai" {
		endpoint = strings.TrimRight(endpoint, "/") + "/chat/completions"
	}
	fingerprint, err := intentDestinationFingerprint(profile, endpoint)
	return settingscontracts.IntentDestination{Endpoint: endpoint, Fingerprint: fingerprint}, err
}

func intentDestinationFingerprint(profile models.Profile, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || profile.ID == "" || profile.Version < 1 || profile.Model == "" {
		return "", ErrIntentMemoryDestination
	}
	key, err := json.Marshal([]any{profile.ID, profile.Version, profile.Protocol, profile.Model, u.Scheme, u.Host, u.EscapedPath()})
	if err != nil {
		return "", ErrIntentMemoryDestination
	}
	hash := sha256.Sum256(key)
	return hex.EncodeToString(hash[:]), nil
}

func (s *Service) settingsAtVersion(ctx context.Context, expected int64) (Snapshot, error) {
	current := s.provider.Current()
	if s.repo != nil {
		revision, found, err := s.repo.Load(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("settings: load revision: %w", err)
		}
		if !found {
			return Snapshot{}, ErrConflict
		}
		current = snapshotOf(revision)
	}
	if current.Version != expected {
		return Snapshot{}, ErrConflict
	}
	return current, nil
}

func storedIntentDestination(stored settingsdomain.Settings) (settingscontracts.IntentDestination, error) {
	id := stored.Models.Defaults["intent"]
	for _, profile := range stored.Models.Profiles {
		if profile.ID == id && profile.Usage == "intent" && profile.Verified() {
			return IntentModelDestination(profile)
		}
	}
	return settingscontracts.IntentDestination{}, ErrIntentMemoryDestination
}

func (s *Service) IntentMemoryRouteCurrent(ctx context.Context, destination settingscontracts.IntentDestination) bool {
	if s.repo == nil || destination.Fingerprint == "" {
		return false
	}
	rev, found, err := s.repo.Load(ctx)
	if err != nil || !found || !rev.Settings.Intent.Enabled {
		return false
	}
	current, err := storedIntentDestination(rev.Settings)
	return err == nil && current == destination
}

func (s *Service) MayDiscloseIntentMemory(ctx context.Context, botID, groupID int64, destination settingscontracts.IntentDestination, requestEndpoint string) bool {
	if s.repo == nil || destination.Fingerprint == "" || destination.Endpoint != requestEndpoint || botID <= 0 || groupID <= 0 {
		return false
	}
	rev, found, err := s.repo.Load(ctx)
	if err != nil || !found || !rev.Settings.Intent.Enabled {
		return false
	}
	current, err := storedIntentDestination(rev.Settings)
	if err != nil || current != destination {
		return false
	}
	_, reason := intentDisclosureState(rev.Settings, botID, groupID, destination, true)
	return reason == ""
}

func intentGrant(stored settingsdomain.Settings, botID, groupID int64, destination string) (settingsdomain.IntentMemoryGrant, bool) {
	for _, grant := range stored.IntentMemoryGrants {
		if grant.BotID == botID && grant.GroupID == groupID && grant.Destination == destination {
			return grant, true
		}
	}
	return settingsdomain.IntentMemoryGrant{}, false
}

func intentDisclosureState(stored settingsdomain.Settings, botID, groupID int64, destination settingscontracts.IntentDestination, memory bool) (int64, string) {
	current, err := storedIntentDestination(stored)
	if err != nil || !stored.Intent.Enabled || current != destination {
		return 0, "background_destination_changed"
	}
	if !memory {
		return 0, ""
	}
	grant, allowed := intentGrant(stored, botID, groupID, destination.Fingerprint)
	if !allowed {
		return 0, "background_disclosure_denied"
	}
	return grant.Generation, ""
}

func (s *Service) IntentDisclosureRevision(ctx context.Context, botID, groupID int64, dest settingscontracts.IntentDestination, memory bool) (int64, string) {
	if s.repo == nil {
		return 0, "background_destination_changed"
	}
	current, found, err := s.repo.Load(ctx)
	if err != nil || !found {
		return 0, "background_destination_changed"
	}
	return intentDisclosureState(current.Settings, botID, groupID, dest, memory)
}

func (s *Service) WithIntentDisclosure(ctx context.Context, botID, groupID int64, dest settingscontracts.IntentDestination, memory bool, revision int64, fn func() error) (reason string, err error) {
	locker, ok := s.repo.(interface {
		WithIntentSettings(context.Context, func(settingsdomain.Revision) error) error
	})
	if !ok {
		return "background_destination_changed", nil
	}
	err = locker.WithIntentSettings(ctx, func(current settingsdomain.Revision) error {
		generation, currentReason := intentDisclosureState(current.Settings, botID, groupID, dest, memory)
		reason = currentReason
		if reason == "" && memory && generation != revision {
			reason = "background_disclosure_denied"
		}
		if reason != "" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn()
	})
	return reason, err
}

func (s *Service) IntentMemoryGrant(ctx context.Context, botID, groupID int64) (bool, string, int64, error) {
	if s.repo == nil {
		return false, "", 0, ErrIntentMemoryDestination
	}
	rev, found, err := s.repo.Load(ctx)
	if err != nil {
		return false, "", 0, err
	}
	if !found {
		return false, "", 0, ErrIntentMemoryDestination
	}
	dest, err := storedIntentDestination(rev.Settings)
	if err != nil {
		return false, "", rev.Version, nil
	}
	_, allowed := intentGrant(rev.Settings, botID, groupID, dest.Fingerprint)
	return allowed, dest.Fingerprint, rev.Version, nil
}

func (s *Service) SetIntentMemoryGrant(ctx context.Context, botID, groupID int64, allow bool, expectedDestination string, version int64, actor string) (int64, error) {
	if s.repo == nil || botID <= 0 || groupID <= 0 {
		return 0, ErrIntentMemoryDestination
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	current, err := s.settingsAtVersion(ctx, version)
	if err != nil {
		return 0, err
	}
	dest, err := storedIntentDestination(current.Settings)
	if allow && (err != nil || expectedDestination != dest.Fingerprint) {
		return 0, ErrIntentMemoryDestination
	}
	next := current.Settings.Clone()
	next.IntentMemoryGrants = slices.DeleteFunc(next.IntentMemoryGrants, func(grant settingsdomain.IntentMemoryGrant) bool {
		return grant.BotID == botID && grant.GroupID == groupID
	})
	if allow {
		grant, exists := intentGrant(current.Settings, botID, groupID, dest.Fingerprint)
		if !exists {
			grant = settingsdomain.IntentMemoryGrant{BotID: botID, GroupID: groupID, Destination: dest.Fingerprint, Generation: version + 1}
		}
		next.IntentMemoryGrants = append(next.IntentMemoryGrants, grant)
	}
	rev, applied, err := s.repo.Update(ctx, next, version, actor)
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, ErrConflict
	}
	s.provider.Store(snapshotOf(rev))
	return rev.Version, nil
}
