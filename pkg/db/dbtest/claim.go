package dbtest

import (
	"context"
	"github.com/HappyLadySauceM/Nagi/pkg/db"
	"testing"
)

// Claim seeds or reads an active claim for repository fixtures.
// Claim 为仓储测试数据建立或读取有效认领。
func Claim(t *testing.T, p *db.Pool, id int64) string {
	t.Helper()
	var token string
	err := p.QueryRow(context.Background(), `UPDATE message_jobs SET status='processing',claim_token=COALESCE(claim_token,gen_random_uuid()),lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id=$1 RETURNING claim_token`, id).Scan(&token)
	if err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	return token
}
