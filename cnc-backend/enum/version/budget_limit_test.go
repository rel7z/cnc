package version

import "testing"

func TestRequestBudgetLimits(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"", 64}, {"1", 1}, {"64", 64}, {"256", 256}} {
		got, err := BudgetLimit(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %d %v", tc.raw, got, err)
		}
		if _, err := NewRequestBudget(got); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"0", "257", "-1", "oops"} {
		if _, err := BudgetLimit(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
