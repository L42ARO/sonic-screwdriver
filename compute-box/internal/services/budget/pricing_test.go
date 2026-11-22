package budget

import (
	"math"
	"testing"
)

func TestCalculateTokenCost(t *testing.T) {
	cases := []struct {
		model                 string
		input, cached, output int
		want                  float64
	}{
		// 90k fresh input at $0.10/M, 10k cached at $0.01/M, 2k output at $0.50/M
		{"gpt-6-luna", 100_000, 10_000, 2_000, (90_000*0.10 + 10_000*0.01 + 2_000*0.50) / 1e6},
		// above the 272k threshold the large rates apply
		{"gpt-6.1-sol", 300_000, 0, 1_000, (300_000*4.00 + 1_000*15.00) / 1e6},
		// cached can't exceed input
		{"gpt-6-luna", 100, 500, 0, 500 * 0.01 / 1e6},
		{"unknown-model", 1_000_000, 0, 1_000_000, 0},
	}
	for _, c := range cases {
		if got := CalculateTokenCost(c.model, c.input, c.cached, c.output); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s(%d,%d,%d) = %v, want %v", c.model, c.input, c.cached, c.output, got, c.want)
		}
	}
	// The conservative figure adds the 0.25x write premium on top.
	base := CalculateTokenCost("gpt-6-luna", 10_000, 0, 0)
	if got := conservativeCost("gpt-6-luna", 10_000, 0, 8_000, 0); math.Abs(got-(base+8_000*0.10*0.25/1e6)) > 1e-12 {
		t.Errorf("conservative = %v", got)
	}
}
