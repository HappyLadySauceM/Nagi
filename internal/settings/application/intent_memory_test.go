package application

import (
	"context"
	"errors"
	"testing"
	"time"

	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

func TestIntentMemoryGrantResistsStaleAndClientWrites(t *testing.T) {
	ctx := context.Background()
	baseline := testSettings()
	profile := models.Profile{ID: "intent-fixture", Usage: "intent", Protocol: "openai", BaseURL: "http://127.0.0.1/one", Model: "fixture", TimeoutSeconds: 10, Version: 1, VerifiedVersion: 1, VerifiedAt: time.Now()}
	baseline.Models = models.Catalog{Initialized: true, Defaults: map[string]string{"intent": profile.ID}, Profiles: []models.Profile{profile}}
	repo := &fakeRepo{}
	writer, _ := newService(t, repo, baseline)
	other, _ := newService(t, repo, baseline)
	if err := writer.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := other.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	_, fp, version, err := writer.IntentMemoryGrant(ctx, 1, 22)
	if err != nil || fp == "" {
		t.Fatalf("destination missing: %v", err)
	}
	if _, err := writer.SetIntentMemoryGrant(ctx, 1, 22, true, "wrong", version, "admin"); !errors.Is(err, ErrIntentMemoryDestination) {
		t.Fatalf("wrong destination = %v", err)
	}
	granted, err := writer.SetIntentMemoryGrant(ctx, 1, 22, true, fp, version, "admin")
	if err != nil {
		t.Fatal(err)
	}
	dest, _ := IntentModelDestination(profile)
	if !other.MayDiscloseIntentMemory(ctx, 1, 22, dest, dest.Endpoint) || other.MayDiscloseIntentMemory(ctx, 1, 23, dest, dest.Endpoint) || other.MayDiscloseIntentMemory(ctx, 2, 22, dest, dest.Endpoint) || other.MayDiscloseIntentMemory(ctx, 1, 22, dest, dest.Endpoint+"/other") {
		t.Fatal("durable grant must match account, group and endpoint")
	}
	if _, err := other.Replace(ctx, baseline, version, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
	next := baseline.Clone()
	next.Role.BotName = "edited"
	next.IntentMemoryGrants = []settingsdomain.IntentMemoryGrant{{BotID: 9, GroupID: 22, Destination: fp}}
	saved, err := other.Replace(ctx, next, granted, "ordinary")
	if err != nil || saved.Settings.IntentMemoryGrants != nil || !writer.MayDiscloseIntentMemory(ctx, 1, 22, dest, dest.Endpoint) || writer.MayDiscloseIntentMemory(ctx, 9, 22, dest, dest.Endpoint) {
		t.Fatalf("ordinary save failed to inherit the grant: %v", err)
	}
	revoked, err := writer.SetIntentMemoryGrant(ctx, 1, 22, false, "", saved.Version, "admin")
	if err != nil {
		t.Fatal(err)
	}
	next.IntentMemoryGrants = []settingsdomain.IntentMemoryGrant{{BotID: 1, GroupID: 22, Destination: fp}}
	if _, err := other.Update(ctx, next, saved.Version, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale settings = %v", err)
	}
	if _, err := other.Update(ctx, next, revoked, "ordinary"); err != nil {
		t.Fatal(err)
	}
	if writer.MayDiscloseIntentMemory(ctx, 1, 22, dest, dest.Endpoint) || repo.revision.Settings.Role.BotName != "edited" {
		t.Fatal("grant was restored or ordinary edit lost")
	}
}

func TestIntentDestinationSeparatesEffectiveRoute(t *testing.T) {
	profile := models.Profile{ID: "fixture", Version: 1, Model: "model", Protocol: "jev", BaseURL: "https://example.invalid/first"}
	baseline, err := IntentModelDestination(profile)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*models.Profile){
		func(p *models.Profile) { p.BaseURL = "https://example.invalid/second" },
		func(p *models.Profile) { p.BaseURL = "https://example.invalid/%66irst" },
		func(p *models.Profile) { p.Protocol = "openai" },
		func(p *models.Profile) { p.Model = "replacement" },
		func(p *models.Profile) { p.Version++ },
		func(p *models.Profile) { p.ID = "replacement" },
	} {
		changed := profile
		mutate(&changed)
		actual, err := IntentModelDestination(changed)
		if err != nil || actual.Fingerprint == baseline.Fingerprint {
			t.Fatalf("route collapsed: %+v %v", actual, err)
		}
	}
	profile.Secret = "new-sensitive-value"
	profile.HTTPHeaders = map[string]string{"Authorization": "new-sensitive-value"}
	if after, err := IntentModelDestination(profile); err != nil || after != baseline {
		t.Fatalf("credentials changed fingerprint: %+v %v", after, err)
	}
}
