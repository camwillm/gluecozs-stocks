package stocks

import (
	"math"
	"testing"
)

func TestComputeView(t *testing.T) {
	tests := []struct {
		name     string
		shares   float64
		buyPrice float64
		price    float64
		wantUSD  float64
		wantPct  float64
	}{
		{"gain", 10, 100, 103, 30, 3},
		{"loss", 10, 100, 95, -50, -5},
		{"zero buy price", 10, 0, 50, 500, 0},
		{"fifty dollar buy", 10, 50, 53, 30, 6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := position{Shares: tc.shares, BuyPrice: tc.buyPrice}
			got := computeView(p, tc.price)

			if math.Abs(got.GainUSD-tc.wantUSD) > 0.0001 {
				t.Errorf("GainUSD = %v, want %v", got.GainUSD, tc.wantUSD)
			}
			if math.Abs(got.GainPct-tc.wantPct) > 0.0001 {
				t.Errorf("GainPct = %v, want %v", got.GainPct, tc.wantPct)
			}
		})
	}
}

func TestComputeSummary(t *testing.T) {
	views := []positionView{
		computeView(position{Shares: 10, BuyPrice: 100}, 110), // +100
		computeView(position{Shares: 5, BuyPrice: 50}, 40),    // -50
	}
	s := computeSummary(views, 3) // one lot didn't get a price this time

	if s.Positions != 3 || s.Priced != 2 {
		t.Fatalf("Positions=%d Priced=%d, want 3 and 2", s.Positions, s.Priced)
	}
	wantCost := 10*100.0 + 5*50.0
	wantValue := 10*110.0 + 5*40.0
	if math.Abs(s.Cost-wantCost) > 0.0001 {
		t.Errorf("Cost = %v, want %v", s.Cost, wantCost)
	}
	if math.Abs(s.Value-wantValue) > 0.0001 {
		t.Errorf("Value = %v, want %v", s.Value, wantValue)
	}
	if math.Abs(s.GainUSD-(wantValue-wantCost)) > 0.0001 {
		t.Errorf("GainUSD = %v, want %v", s.GainUSD, wantValue-wantCost)
	}
}
