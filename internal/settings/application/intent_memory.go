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
	return slices.Contains(rev.Settings.IntentMemoryGrants, settingsdomain.IntentMemoryGrant{BotID: botID, GroupID: groupID, Destination: destination.Fingerprint})
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
	grant := settingsdomain.IntentMemoryGrant{BotID: botID, GroupID: groupID, Destination: dest.Fingerprint}
	return slices.Contains(rev.Settings.IntentMemoryGrants, grant), dest.Fingerprint, rev.Version, nil
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
		next.IntentMemoryGrants = append(next.IntentMemoryGrants, settingsdomain.IntentMemoryGrant{BotID: botID, GroupID: groupID, Destination: dest.Fingerprint})
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
