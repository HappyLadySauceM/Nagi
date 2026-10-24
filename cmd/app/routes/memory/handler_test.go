// Tests for the PATCH status transition validation.
// PATCH 状态变更校验的测试。
package memory

import "testing"

// TestParseStatusAction pins the accepted transitions. candidate and superseded
// are valid memory states but cannot be reached through the management API, so
// they must be rejected instead of silently ignored.
// TestParseStatusAction 固定可接受的状态变更：candidate 与 superseded 虽然合法，
// 但管理接口无法抵达，必须拒绝而不是静默忽略。
func TestParseStatusAction(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   statusAction
		ok     bool
	}{
		{name: "keep current status", status: "", want: statusNone, ok: true},
		{name: "approve", status: "active", want: statusApprove, ok: true},
		{name: "soft delete", status: "deleted", want: statusDelete, ok: true},
		{name: "candidate is not reachable", status: "candidate", want: statusNone, ok: false},
		{name: "superseded is not reachable", status: "superseded", want: statusNone, ok: false},
		{name: "unknown status", status: "archived", want: statusNone, ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseStatusAction(tc.status)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("parseStatusAction(%q) = (%d,%v), want (%d,%v)", tc.status, got, ok, tc.want, tc.ok)
			}
		})
	}
}
