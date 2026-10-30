// Tests for the settings service: seeding, optimistic locking and reload.
// 配置服务的测试：初始化、乐观锁与重载。
package application

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	settingsdomain "github.com/HappyLadySauceM/Nagi/internal/settings/domain"
)

type fakeRepo struct {
	revision settingsdomain.Revision
	exists   bool
	inserts  int
	updates  int
}

func (f *fakeRepo) Load(context.Context) (settingsdomain.Revision, bool, error) {
	return f.revision, f.exists, nil
}

func (f *fakeRepo) Insert(_ context.Context, settings settingsdomain.Settings, actor string) (settingsdomain.Revision, error) {
	f.inserts++
	if f.exists {
		return f.revision, nil
	}
	f.exists = true
	f.revision = settingsdomain.Revision{Settings: settings, Version: 1, UpdatedAt: time.Now(), UpdatedBy: actor}
	return f.revision, nil
}

func (f *fakeRepo) Update(_ context.Context, settings settingsdomain.Settings, expected int64, actor string) (settingsdomain.Revision, bool, error) {
	if !f.exists || f.revision.Version != expected {
		return settingsdomain.Revision{}, false, nil
	}
	f.updates++
	f.revision = settingsdomain.Revision{
		Settings: settings, Version: expected + 1, UpdatedAt: time.Now(), UpdatedBy: actor,
	}
	return f.revision, true, nil
}

func (f *fakeRepo) Version(context.Context) (int64, bool, error) {
	return f.revision.Version, f.exists, nil
}

func testSettings() settingsdomain.Settings {
	return settingsdomain.Settings{
		AI: settingsdomain.AI{
			Enabled: true, BaseURL: "https://llm.example/v1", Model: "m",
			MaxOutputTokens: 400, GenerationTimeoutSeconds: 25,
		},
		Intent: settingsdomain.Intent{
			Enabled: true, Provider: "jev", Endpoint: "https://api.example/v1/systemone",
			Model: "jev", TimeoutSeconds: 3, ContinuationThreshold: 0.8,
			InterjectionThreshold: 0.95, AddressedOtherMax: 0.6, HistoryLimit: 30,
		},
		Agent: settingsdomain.Agent{
			MaxSteps: 6, MaxToolCalls: 5, MaxRunDurationSeconds: 30,
			Sending: settingsdomain.Sending{MaxMessagesPerRun: 0, MaxCharsPerMessage: 4500, MaxTotalCharsPerRun: 0},
		},
		Messages: settingsdomain.Messages{RetentionDays: 30},
		Tools: settingsdomain.Tools{
			TimeoutSeconds: 5, MaxResultBytes: 8192,
			PermissionDefaults: map[string]bool{},
		},
	}
}

func newService(t *testing.T, repo Repository, defaults settingsdomain.Settings) (*Service, *Provider) {
	t.Helper()
	provider := NewProvider(Snapshot{Settings: defaults.Clone()})
	service, err := NewService(Options{
		Repository:  repo,
		Provider:    provider,
		Defaults:    defaults,
		Providers:   []string{"builtin.time"},
		Credentials: Credentials{AI: true},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, provider
}

func TestEnsureSeedsDefaultsWhenMissing(t *testing.T) {
	// 表为空时用配置文件基线写入初值，并把快照标记为已持久化。
	repo := &fakeRepo{}
	service, provider := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if repo.inserts != 1 || provider.Current().Version != 1 || !provider.Current().Persisted {
		t.Fatalf("seed = %+v", provider.Current())
	}
}

func TestEnsureWithoutRepositoryKeepsBaseline(t *testing.T) {
	// 没有数据库时快照仍然可用，但标记为未持久化。
	service, provider := newService(t, nil, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if provider.Current().Persisted {
		t.Fatal("snapshot without a repository must not claim persistence")
	}
}

func TestUpdateBumpsVersionAndSwapsSnapshot(t *testing.T) {
	// 保存后版本递增，实时快照立即反映新值。
	repo := &fakeRepo{}
	service, provider := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	next := testSettings()
	next.Agent.Sending.MaxMessagesPerRun = 5
	view, err := service.Update(context.Background(), next, 1, "admin")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if view.Version != 2 || provider.Settings().Agent.Sending.MaxMessagesPerRun != 5 {
		t.Fatalf("view = %+v provider = %+v", view, provider.Current())
	}
}

func TestUpdateRejectsStaleVersion(t *testing.T) {
	// 版本不一致返回冲突，且不改动实时快照。
	repo := &fakeRepo{}
	service, provider := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	before := provider.Settings()
	next := testSettings()
	next.Agent.MaxSteps = 9
	if _, err := service.Update(context.Background(), next, 99, "admin"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if provider.Settings().Agent.MaxSteps != before.Agent.MaxSteps {
		t.Fatal("conflicting update must not change the live snapshot")
	}
}

func TestUpdateRejectsInvalidSettings(t *testing.T) {
	// 校验失败不落库，也不改快照。
	repo := &fakeRepo{}
	service, _ := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	next := testSettings()
	next.Agent.MaxSteps = 0
	if _, err := service.Update(context.Background(), next, 1, "admin"); err == nil {
		t.Fatal("expected a validation error")
	}
	if repo.updates != 0 {
		t.Fatal("invalid settings must not be persisted")
	}
}

func TestUpdateRejectsAIWithoutCredential(t *testing.T) {
	// 配置文件缺少密钥时不允许在界面上启用生成模型。
	repo := &fakeRepo{}
	provider := NewProvider(Snapshot{Settings: testSettings()})
	service, err := NewService(Options{
		Repository: repo, Provider: provider, Defaults: testSettings(),
		Credentials: Credentials{AI: false},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	next := testSettings()
	if _, err := service.Update(context.Background(), next, 1, "admin"); err == nil {
		t.Fatal("expected a credential error")
	}
}

func TestResetRestoresDefaults(t *testing.T) {
	// 恢复默认写回基线并生成新修订。
	repo := &fakeRepo{}
	service, provider := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	changed := testSettings()
	changed.Agent.MaxSteps = 9
	if _, err := service.Update(context.Background(), changed, 1, "admin"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := service.Reset(context.Background(), "admin"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if provider.Settings().Agent.MaxSteps != testSettings().Agent.MaxSteps {
		t.Fatalf("max steps = %d", provider.Settings().Agent.MaxSteps)
	}
	if provider.Current().Version != 3 {
		t.Fatalf("version = %d, want 3", provider.Current().Version)
	}
}

func TestReloadIfChangedPicksUpForeignRevision(t *testing.T) {
	// 其它副本保存后，轮询把新修订读进本进程快照。
	repo := &fakeRepo{}
	service, provider := newService(t, repo, testSettings())
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	foreign := testSettings()
	foreign.Intent.Enabled = false
	repo.revision = settingsdomain.Revision{Settings: foreign, Version: 7, UpdatedAt: time.Now(), UpdatedBy: "other"}
	if err := service.reloadIfChanged(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if provider.Current().Version != 7 || provider.Settings().Intent.Enabled {
		t.Fatalf("snapshot = %+v", provider.Current())
	}
}
