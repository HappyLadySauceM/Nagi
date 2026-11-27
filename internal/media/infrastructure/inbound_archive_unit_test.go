package infrastructure

import (
	"context"
	"testing"

	scope "github.com/HappyLadySauceM/Nagi/internal/scope/domain"
)

type inboundArchiveAuth struct{ allowed bool }

func (s *inboundArchiveAuth) Resolve(context.Context, int64, scope.ScopeType, int64) (scope.Policy, error) {
	return scope.Policy{Enabled: s.allowed, StoreMessages: s.allowed}, nil
}

// TestInboundArchiveAllowedUsesScopeMock verifies store gating without PostgreSQL.
// TestInboundArchiveAllowedUsesScopeMock 在无 PostgreSQL 时验证 store 门控。
func TestInboundArchiveAllowedUsesScopeMock(t *testing.T) {
	auth := &inboundArchiveAuth{allowed: false}
	archive := &InboundArchive{scopes: auth}
	task := archiveTask{BotID: 1, TargetID: 2, Scope: string(scope.ScopeGroup)}
	allowed, err := archive.allowed(context.Background(), task)
	if err != nil || allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
	auth.allowed = true
	allowed, err = archive.allowed(context.Background(), task)
	if err != nil || !allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
}
