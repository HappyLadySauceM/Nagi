package application

import (
	"context"
	"encoding/json"
	"fmt"
	models "github.com/HappyLadySauceM/Nagi/internal/models/contracts"
	vault "github.com/HappyLadySauceM/Nagi/internal/models/infrastructure"
	"strings"
	"testing"
	"time"
)

type testModelTester struct{ err error }

func (t testModelTester) TestModel(context.Context, models.Resolved) error { return t.err }

func modelService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newService(t, &fakeRepo{}, testSettings())
	v, err := vault.NewVault()
	if err != nil {
		t.Fatal(err)
	}
	s.cipher = v
	s.modelTester = testModelTester{}
	s.legacyAIKey = "old-token"
	s.legacyIntentKey = "intent-token"
	if err = s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Models().Catalog.Profiles {
		if _, err = s.SaveModel(context.Background(), p.ID, ModelEdit{Profile: p, Version: s.Models().Version}, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// TestModelCredentialsSurviveEditsAndReset verifies safe views, replacement, clear and one-time import.
// TestModelCredentialsSurviveEditsAndReset 验证安全视图、替换、清除及一次性导入。
func TestModelCredentialsSurviveEditsAndReset(t *testing.T) {
	s := modelService(t)
	ctx := context.Background()
	v := s.Models()
	if len(v.Catalog.Profiles) != 2 {
		t.Fatal("import missing")
	}
	p := v.Catalog.Profiles[0]
	for _, data := range []any{v, s.View()} {
		b, _ := json.Marshal(data)
		if strings.Contains(string(b), "old-token") || strings.Contains(string(b), "Secret\"") {
			t.Fatal("credential or ciphertext in public view")
		}
	}
	key := "new-token"
	v, err := s.SaveModel(ctx, p.ID, ModelEdit{Profile: p, Credential: &key, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	p = v.Catalog.Profiles[0]
	p.Name = "renamed"
	p.Thinking = true
	p.ReasoningEffort = "high"
	p.ContextWindow = 128000
	v, err = s.SaveModel(ctx, p.ID, ModelEdit{Profile: p, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveModel(ctx, "generation", 1, "group", 1)
	if err != nil || resolved.APIKey != key || !resolved.Profile.Thinking || resolved.Profile.ReasoningEffort != "high" || resolved.Profile.ContextWindow != 128000 || resolved.Profile.TimeoutSeconds != 60 {
		t.Fatal("credential not retained")
	}
	if _, err = s.SaveModel(ctx, p.ID, ModelEdit{Profile: p, Version: v.Version - 1}, "stale"); err != ErrConflict {
		t.Fatal("stale save accepted")
	}
	v, err = s.SaveModel(ctx, p.ID, ModelEdit{Profile: p, ClearCredential: true, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reset(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	resolved, err = s.ResolveModel(ctx, "generation", 1, "group", 1)
	if err != nil || resolved.APIKey != "" {
		t.Fatal("reset/restart resurrected credential")
	}
}

// TestConversationModelPrecedence verifies account/kind isolation and in-use deletion protection.
// TestConversationModelPrecedence 验证账号和会话类型隔离及在用模型删除保护。
func TestConversationModelPrecedence(t *testing.T) {
	s := modelService(t)
	ctx := context.Background()
	v := s.Models()
	p := models.Profile{Name: "second", Usage: "generation", Protocol: "openai", BaseURL: "https://other.example/v1", Model: "second", Tools: true, MaxOutputTokens: 500, TimeoutSeconds: 30}
	v, err := s.SaveModel(ctx, "", ModelEdit{Profile: p, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := v.Catalog.Profiles[len(v.Catalog.Profiles)-1].ID
	if err = s.SelectModel(ctx, 1, "group", 0, id, v.Version, "test"); err != nil {
		t.Fatal(err)
	}
	r, _ := s.ResolveModel(ctx, "generation", 1, "group", 999)
	if r.Profile.ID != id {
		t.Fatal("template selection lost")
	}
	r, _ = s.ResolveModel(ctx, "generation", 2, "group", 999)
	if r.Profile.ID == id {
		t.Fatal("account leaked")
	}
	r, _ = s.ResolveModel(ctx, "generation", 1, "private", 999)
	if r.Profile.ID == id {
		t.Fatal("kind leaked")
	}
	if _, err = s.DeleteModel(ctx, id, s.Models().Version, "test"); err == nil {
		t.Fatal("referenced model deleted")
	}
	global := s.Models().Catalog.Defaults["generation"]
	if err = s.SelectModel(ctx, 1, "group", 999, global, s.Models().Version, "test"); err != nil {
		t.Fatal(err)
	}
	r, _ = s.ResolveModel(ctx, "generation", 1, "group", 999)
	if r.Profile.ID != global {
		t.Fatal("specific selection ignored")
	}
}

// TestModelSnapshotIncludesMediaCredentials verifies hot edits affect only subsequent message runs.
// TestModelSnapshotIncludesMediaCredentials 验证媒体模型及凭证热修改只影响后续消息运行。
func TestModelSnapshotIncludesMediaCredentials(t *testing.T) {
	s := modelService(t)
	ctx := context.Background()
	v := s.Models()
	oldKey := "old-asr-key"
	var err error
	v, err = s.SaveModel(ctx, "", ModelEdit{Profile: models.Profile{Name: "ASR", Usage: "asr", Protocol: "openai", BaseURL: "https://asr.example/v1", Model: "old-asr", TimeoutSeconds: 30}, Credential: &oldKey, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	p := v.Catalog.Profiles[len(v.Catalog.Profiles)-1]
	oldCookie := "old-cookie"
	v, err = s.ConfigureModels(ctx, map[string]string{"asr": p.ID}, &oldCookie, false, v.Version, "test")
	if err != nil {
		t.Fatal(err)
	}
	frozen := s.ModelSnapshot(ctx)
	p.Model = "new-asr"
	newKey := "new-asr-key"
	v, err = s.SaveModel(ctx, p.ID, ModelEdit{Profile: p, Credential: &newKey, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	newCookie := "new-cookie"
	if _, err = s.ConfigureModels(ctx, nil, &newCookie, false, v.Version, "test"); err != nil {
		t.Fatal(err)
	}
	old, err := s.ResolveModel(s.ModelSnapshot(frozen), "asr", 1, "private", 1)
	if err != nil || old.Profile.Model != "old-asr" || old.APIKey != oldKey {
		t.Fatal("in-flight media model changed")
	}
	fresh, err := s.ResolveModel(s.ModelSnapshot(ctx), "asr", 1, "private", 1)
	if err != nil || fresh.Profile.Model != "new-asr" || fresh.APIKey != newKey {
		t.Fatal("next run did not receive hot edit")
	}
	if cookie, _ := s.BilibiliCredential(frozen); cookie != oldCookie {
		t.Fatal("in-flight cookie changed")
	}
	if cookie, _ := s.BilibiliCredential(ctx); cookie != newCookie {
		t.Fatal("next run did not receive new cookie")
	}
}

// TestModelProbeFailureAndVerification verifies failed testing never persists or revives a credential.
// TestModelProbeFailureAndVerification 验证测试失败不落库且验证信息由服务端生成。
func TestModelProbeFailureAndVerification(t *testing.T) {
	s := modelService(t)
	v := s.Models()
	p := v.Catalog.Profiles[0]
	s.modelTester = testModelTester{fmt.Errorf("upstream unavailable")}
	key := "replacement"
	_, err := s.SaveModel(context.Background(), p.ID, ModelEdit{Profile: p, Credential: &key, Version: v.Version}, "test")
	if err == nil || s.Models().Version != v.Version {
		t.Fatal("failed test persisted")
	}
	resolved, err := s.ResolveModel(context.Background(), "generation", 1, "private", 1)
	if err != nil || resolved.APIKey == key {
		t.Fatal("failed test replaced credential")
	}
	settings := s.provider.Settings().Clone()
	settings.Models.Profiles[0].VerifiedVersion = 0
	s.provider.Store(Snapshot{Settings: settings, Version: v.Version})
	if _, err = s.ResolveModel(context.Background(), "generation", 1, "private", 1); err == nil {
		t.Fatal("old unverified model remained usable")
	}
	s.modelTester = testModelTester{}
	p.VerifiedVersion = 999
	p.VerifiedAt = time.Now().Add(time.Hour)
	saved, err := s.SaveModel(context.Background(), p.ID, ModelEdit{Profile: p, Version: v.Version}, "test")
	if err != nil {
		t.Fatal(err)
	}
	got := saved.Catalog.Profiles[0]
	if !got.Verified() || got.VerifiedAt.After(time.Now()) {
		t.Fatal("client forged verification")
	}
}

type blockingModelTester struct{ entered, release chan struct{} }

func (t blockingModelTester) TestModel(ctx context.Context, _ models.Resolved) error {
	close(t.entered)
	select {
	case <-t.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TestConcurrentModelEditConflictsAfterProbe verifies the probe holds no global settings lock.
// TestConcurrentModelEditConflictsAfterProbe 验证测试不占全局配置锁且测试后仍检查版本冲突。
func TestConcurrentModelEditConflictsAfterProbe(t *testing.T) {
	s := modelService(t)
	v := s.Models()
	probe := blockingModelTester{make(chan struct{}), make(chan struct{})}
	s.modelTester = probe
	done := make(chan error, 1)
	go func() {
		_, err := s.SaveModel(context.Background(), v.Catalog.Profiles[0].ID, ModelEdit{Profile: v.Catalog.Profiles[0], Version: v.Version}, "test")
		done <- err
	}()
	<-probe.entered
	if _, err := s.ConfigureModels(context.Background(), nil, nil, false, v.Version, "concurrent"); err != nil {
		t.Fatal(err)
	}
	close(probe.release)
	if err := <-done; err != ErrConflict {
		t.Fatal("probe overwrote concurrent edit", err)
	}
}

type headerProbe struct {
	headers map[string]string
	err     error
}

func (p *headerProbe) TestModel(_ context.Context, m models.Resolved) error {
	p.headers = m.Profile.HTTPHeaders
	return p.err
}

func TestModelPrivateHeadersLifecycle(t *testing.T) {
	s := modelService(t)
	ctx := context.Background()
	v := s.Models()
	p := v.Catalog.Profiles[0]
	probe := &headerProbe{}
	s.modelTester = probe
	p.HTTPHeaders = map[string]string{"X-Private": "header-private-value", "Authorization": "custom-token"}
	p.HeaderSecrets = map[string]string{"Injected": "fake-ciphertext"}
	save := func(profile models.Profile) (ModelsView, error) {
		return s.SaveModel(ctx, profile.ID, ModelEdit{Profile: profile, Version: s.Models().Version}, "test")
	}
	v, err := save(p)
	if err != nil {
		t.Fatal(err)
	}
	if probe.headers["X-Private"] != "header-private-value" {
		t.Fatal("probe missing header")
	}
	for _, view := range []any{v, s.View(), s.provider.Settings().Models} {
		b, _ := json.Marshal(view)
		if strings.Contains(string(b), "header-private-value") || strings.Contains(string(b), "custom-token") || strings.Contains(string(b), "fake-ciphertext") {
			t.Fatal("private header leaked")
		}
	}
	p = v.Catalog.Profiles[0]
	if len(p.HTTPHeaders) != 2 || len(p.HeaderSecrets) != 0 || p.HTTPHeaders["X-Private"] != "" {
		t.Fatal("unsafe public headers")
	}
	snapshot := s.ModelSnapshot(ctx)
	p.Name = "retain headers"
	v, err = save(p)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveModel(ctx, "generation", 1, "private", 1)
	if err != nil || resolved.Profile.HTTPHeaders["X-Private"] != "header-private-value" {
		t.Fatal("blank draft did not retain header")
	}
	p = v.Catalog.Profiles[0]
	p.HTTPHeaders = map[string]string{"X-Private": "replacement"}
	probe.err = fmt.Errorf("upstream failure")
	if _, err = save(p); err == nil {
		t.Fatal("failed probe saved")
	}
	resolved, _ = s.ResolveModel(ctx, "generation", 1, "private", 1)
	if resolved.Profile.HTTPHeaders["X-Private"] != "header-private-value" {
		t.Fatal("failure overwrote headers")
	}
	probe.err = nil
	v, err = save(p)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ = s.ResolveModel(snapshot, "generation", 1, "private", 1)
	if resolved.Profile.HTTPHeaders["X-Private"] != "header-private-value" {
		t.Fatal("snapshot changed")
	}
	p = v.Catalog.Profiles[0]
	p.HTTPHeaders = nil
	if _, err = save(p); err != nil {
		t.Fatal(err)
	}
	resolved, _ = s.ResolveModel(ctx, "generation", 1, "private", 1)
	if len(resolved.Profile.HTTPHeaders) != 0 {
		t.Fatal("header removal ignored")
	}
}
