// Unit tests for the outbound ledger model and run budget.
// 出站台账模型与回合预算的单元测试。
package domain

import (
	"errors"
	"testing"
)

// TestCheckBudgetReportsExhaustedDimension verifies every limit boundary and
// that the rejection identifies the dimension that was exhausted.
// TestCheckBudgetReportsExhaustedDimension 验证每项上限边界，
// 且拒绝结果标明耗尽的维度。
func TestCheckBudgetReportsExhaustedDimension(t *testing.T) {
	limits := Limits{MaxMessages: 3, MaxCharsPerMessage: 10, MaxTotalChars: 20}
	cases := []struct {
		name      string
		used      Usage
		text      string
		wantDim   Dimension
		wantLimit int
		wantReq   int
	}{
		{
			name: "fits exactly", used: Usage{Messages: 2, Chars: 10}, text: "0123456789",
		},
		{
			name: "message too long", used: Usage{}, text: "01234567890",
			wantDim: DimensionMessageChars, wantLimit: 10, wantReq: 11,
		},
		{
			name: "run message count exhausted", used: Usage{Messages: 3, Chars: 0}, text: "hi",
			wantDim: DimensionMessages, wantLimit: 3, wantReq: 4,
		},
		{
			name: "run total chars exceeded", used: Usage{Messages: 1, Chars: 15}, text: "0123456",
			wantDim: DimensionTotalChars, wantLimit: 20, wantReq: 22,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckBudget(limits, tc.used, tc.text)
			if tc.wantDim == "" {
				if err != nil {
					t.Fatalf("budget must accept the message: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrBudgetExceeded) {
				t.Fatalf("error = %v, want ErrBudgetExceeded", err)
			}
			var budget *BudgetError
			if !errors.As(err, &budget) {
				t.Fatalf("error = %v, want *BudgetError", err)
			}
			if budget.Dimension != tc.wantDim || budget.Limit != tc.wantLimit || budget.Requested != tc.wantReq {
				t.Fatalf("budget = %+v, want %s/%d/%d", budget, tc.wantDim, tc.wantLimit, tc.wantReq)
			}
		})
	}
}

// TestCheckBudgetCountsRunes verifies limits use Unicode characters, not bytes,
// so Chinese text is not rejected three times too early.
// TestCheckBudgetCountsRunes 验证上限按 Unicode 字符而非字节计算，
// 中文文本不会提前三倍被拒。
func TestCheckBudgetCountsRunes(t *testing.T) {
	limits := Limits{MaxMessages: 2, MaxCharsPerMessage: 4, MaxTotalChars: 8}
	if err := CheckBudget(limits, Usage{}, "一二三四"); err != nil {
		t.Fatalf("four runes must fit a four-rune limit: %v", err)
	}
	if err := CheckBudget(limits, Usage{}, "一二三四五"); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("five runes must exceed the limit: %v", err)
	}
}

// TestStatusTransitions verifies which states count as delivered and which
// states a sending record may settle into.
// TestStatusTransitions 验证哪些状态算已投递，以及 sending 记录可落定到哪些状态。
func TestStatusTransitions(t *testing.T) {
	for _, status := range []Status{StatusPrepared, StatusSending, StatusFailed, StatusDenied, StatusCancelled} {
		if status.Delivered() {
			t.Fatalf("%s must not count as delivered", status)
		}
	}
	for _, status := range []Status{StatusSent, StatusUnknown} {
		if !status.Delivered() {
			t.Fatalf("%s must count as delivered", status)
		}
	}
	for _, status := range []Status{StatusSent, StatusFailed, StatusUnknown} {
		if !status.Settleable() {
			t.Fatalf("%s must be a settle target", status)
		}
	}
	for _, status := range []Status{StatusPrepared, StatusSending, StatusDenied, StatusCancelled} {
		if status.Settleable() {
			t.Fatalf("%s must not be a settle target", status)
		}
	}
	if Status("bogus").Valid() {
		t.Fatal("unknown status must be rejected")
	}
}
