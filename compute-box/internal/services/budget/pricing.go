// Package budget prices model calls, records them, and enforces the spending cap.
package budget

// Pricing is dollars per million tokens. Above ContextThreshold input tokens the Large
// rates apply.
type Pricing struct {
	ContextThreshold      int
	InputPerMillion       float64
	CachedPerMillion      float64
	OutputPerMillion      float64
	LargeInputPerMillion  float64
	LargeCachedPerMillion float64
	LargeOutputPerMillion float64
}

var pricing = map[string]Pricing{
	"gpt-6.1-sol": {
		ContextThreshold:      272_000,
		InputPerMillion:       2.00,
		CachedPerMillion:      0.10,
		OutputPerMillion:      10.00,
		LargeInputPerMillion:  4.00,
		LargeCachedPerMillion: 0.20,
		LargeOutputPerMillion: 15.00,
	},
	"gpt-6-luna": {
		ContextThreshold:      272_000,
		InputPerMillion:       0.10,
		CachedPerMillion:      0.01,
		OutputPerMillion:      0.50,
		LargeInputPerMillion:  0.20,
		LargeCachedPerMillion: 0.02,
		LargeOutputPerMillion: 0.75,
	},
}

// KnownModel reports whether we have prices for a model. Unpriced models are refused,
// since the budget cap can't be enforced for them.
func KnownModel(model string) bool {
	_, ok := pricing[model]
	return ok
}

// GetModelPricing returns a model's rates (zero rates if unknown).
func GetModelPricing(model string) Pricing { return pricing[model] }

// CalculateTokenCost is the agreed cost formula, in dollars.
func CalculateTokenCost(model string, input, cached, output int) float64 {
	rate := GetModelPricing(model)

	inputRate := rate.InputPerMillion
	cachedRate := rate.CachedPerMillion
	outputRate := rate.OutputPerMillion

	if rate.ContextThreshold > 0 && input > rate.ContextThreshold {
		if rate.LargeInputPerMillion > 0 {
			inputRate = rate.LargeInputPerMillion
		}
		if rate.LargeCachedPerMillion > 0 {
			cachedRate = rate.LargeCachedPerMillion
		}
		if rate.LargeOutputPerMillion > 0 {
			outputRate = rate.LargeOutputPerMillion
		}
	}

	uncachedInput := input - cached
	if uncachedInput < 0 {
		uncachedInput = 0
	}

	cost := (float64(uncachedInput)*inputRate +
		float64(cached)*cachedRate +
		float64(output)*outputRate) / 1_000_000.0

	return cost
}

// conservativeCost is CalculateTokenCost plus OpenAI's cache-write premium: tokens written
// to the cache bill at 1.25x the input rate (30-minute cache) instead of 1x. The cap is
// enforced on this figure so the agreed formula can never undercount past the limit.
func conservativeCost(model string, input, cached, cacheWrite, output int) float64 {
	cost := CalculateTokenCost(model, input, cached, output)
	rate := GetModelPricing(model)
	inputRate := rate.InputPerMillion
	if rate.ContextThreshold > 0 && input > rate.ContextThreshold && rate.LargeInputPerMillion > 0 {
		inputRate = rate.LargeInputPerMillion
	}
	return cost + float64(cacheWrite)*inputRate*0.25/1_000_000.0
}
