// Package testsupport provides shared in-memory fakes for unit tests.
// testsupport 包提供供单元测试复用的内存版 fake。
package testsupport

import (
	"context"
	"fmt"

	"github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

// ScopeRepo is an in-memory scope policy repository.
// ScopeRepo 是内存版作用域策略仓库。
type ScopeRepo struct {
	stored map[string]domain.Policy
}

// NewScopeRepo builds an empty scope repository.
// NewScopeRepo 构造空的作用域仓库。
func NewScopeRepo() *ScopeRepo {
	return &ScopeRepo{stored: make(map[string]domain.Policy)}
}

func (f *ScopeRepo) key(botID int64, t domain.ScopeType, id int64) string {
	return fmt.Sprintf("%d:%s:%d", botID, t, id)
}

func (f *ScopeRepo) Get(_ context.Context, botID int64, t domain.ScopeType, id int64) (domain.Policy, bool, error) {
	p, ok := f.stored[f.key(botID, t, id)]
	return p, ok, nil
}

func (f *ScopeRepo) List(_ context.Context, _ *int64) ([]domain.Policy, error) {
	out := make([]domain.Policy, 0, len(f.stored))
	for _, p := range f.stored {
		out = append(out, p)
	}
	return out, nil
}

func (f *ScopeRepo) Upsert(_ context.Context, botID int64, p domain.Policy) error {
	f.stored[f.key(botID, p.ScopeType, p.ScopeID)] = p
	return nil
}

func (f *ScopeRepo) Delete(_ context.Context, botID int64, t domain.ScopeType, id int64) (bool, error) {
	key := f.key(botID, t, id)
	_, ok := f.stored[key]
	delete(f.stored, key)
	return ok, nil
}

func (f *ScopeRepo) ResolveMemoryAuthorization(_ context.Context, botID int64, t domain.ScopeType, id int64) (int64, bool, error) {
	p := f.stored[f.key(botID, t, id)]
	return 0, p.Enabled && p.StoreMessages && p.AllowMemoryWrite, nil
}

// SetPolicy seeds one policy row for tests.
// SetPolicy 为测试写入一条策略。
func (f *ScopeRepo) SetPolicy(botID int64, p domain.Policy) error {
	return f.Upsert(context.Background(), botID, p)
}
