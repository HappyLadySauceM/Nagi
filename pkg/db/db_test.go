package db

import (
	"context"
	"testing"
)

func TestConnectRejectsUnboundedPoolBeforeDial(t *testing.T) {
	for _, limit := range []int32{-1, 0, 13} {
		pool, err := Connect(context.Background(), "", limit)
		if err == nil || pool != nil {
			t.Fatalf("limit %d: pool=%v error=%v", limit, pool, err)
		}
	}
}
