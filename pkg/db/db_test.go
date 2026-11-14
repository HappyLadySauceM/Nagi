package db

import (
	"context"
	"strings"
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

func TestValidateServerVersion(t *testing.T) {
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{name: "below PostgreSQL 17", version: 169999, wantErr: true},
		{name: "PostgreSQL 17 lower bound", version: 170000, wantErr: false},
		{name: "PostgreSQL 17 upper range", version: 179999, wantErr: false},
		{name: "PostgreSQL 18 lower bound", version: 180000, wantErr: false},
		{name: "PostgreSQL 18 upper range", version: 180999, wantErr: false},
		{name: "PostgreSQL 19 lower bound", version: 190000, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Fatalf("version %d: err=%v, wantErr=%v", tt.version, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "PostgreSQL 17 or 18 required") {
				t.Fatalf("version %d: unexpected error text: %v", tt.version, err)
			}
		})
	}
}
