package stocks

// positionView is a lot with its current price and gain filled in.
type positionView struct {
	position
	Price   float64
	GainUSD float64
	GainPct float64
}

func computeView(p position, price float64) positionView {
	gainUSD := (price - p.BuyPrice) * p.Shares

	var gainPct float64
	if p.BuyPrice != 0 {
		gainPct = gainUSD / (p.BuyPrice * p.Shares) * 100
	}

	return positionView{
		position: p,
		Price:    price,
		GainUSD:  gainUSD,
		GainPct:  gainPct,
	}
}

// summary is the whole portfolio's totals.
type summary struct {
	Positions int // lots in the database
	Priced    int // lots that got a price this time
	Cost      float64
	Value     float64
	GainUSD   float64
	GainPct   float64
}

func computeSummary(views []positionView, total int) summary {
	s := summary{Positions: total, Priced: len(views)}
	for _, v := range views {
		s.Cost += v.BuyPrice * v.Shares
		s.Value += v.Price * v.Shares
	}
	s.GainUSD = s.Value - s.Cost
	if s.Cost != 0 {
		s.GainPct = s.GainUSD / s.Cost * 100
	}
	return s
}
