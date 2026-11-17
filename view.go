package stocks

import (
	"fmt"
	"strconv"
	"strings"
)

// view feeds templates/stocks.html.
type view struct {
	HaveData  bool
	Updated   string
	FormError string

	Positions []positionRow
	Summary   summaryRow
}

type positionRow struct {
	ID       int64
	Ticker   string
	Shares   string
	BuyPrice string
	BoughtAt string
	Price    string
	GainUSD  string
	GainPct  string
	Warn     bool
}

type summaryRow struct {
	Positions int
	Priced    int
	Cost      string
	Value     string
	GainUSD   string
	GainPct   string
	Warn      bool
}

// view builds the page's view data from the latest cached snapshot. It
// never fetches a fresh quote itself; the background poller (or a manual
// "Refresh now") is what keeps the snapshot current, so loading the page
// never waits on Finnhub.
func (m *Module) view() view {
	m.mu.RLock()
	views := append([]positionView(nil), m.lastViews...)
	sum := m.lastSummary
	updated := m.updated
	have := m.haveData
	m.mu.RUnlock()

	v := view{HaveData: have}
	if have {
		v.Updated = updated.Format("3:04:05 PM")
	}
	for _, pv := range views {
		v.Positions = append(v.Positions, positionRow{
			ID:       pv.ID,
			Ticker:   pv.Ticker,
			Shares:   trimZeros(pv.Shares),
			BuyPrice: commas(pv.BuyPrice),
			BoughtAt: pv.BoughtAt.Format("2006-01-02"),
			Price:    commas(pv.Price),
			GainUSD:  fmt.Sprintf("%+.2f", pv.GainUSD),
			GainPct:  fmt.Sprintf("%+.1f%%", pv.GainPct),
			Warn:     pv.GainUSD < 0,
		})
	}
	v.Summary = summaryRow{
		Positions: sum.Positions,
		Priced:    sum.Priced,
		Cost:      commas(sum.Cost),
		Value:     commas(sum.Value),
		GainUSD:   fmt.Sprintf("%+.2f", sum.GainUSD),
		GainPct:   fmt.Sprintf("%+.1f%%", sum.GainPct),
		Warn:      sum.GainUSD < 0,
	}
	return v
}

// commas formats a dollar amount with thousands separators and two decimal
// places, e.g. 12345.6 -> "12,345.60".
func commas(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	whole, frac, _ := strings.Cut(s, ".")

	var out []byte
	for i, c := range []byte(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	res := string(out) + "." + frac
	if neg {
		res = "-" + res
	}
	return res
}

// trimZeros formats a share count without unnecessary trailing zeros:
// 10 -> "10", 2.5 -> "2.5".
func trimZeros(f float64) string {
	s := strconv.FormatFloat(f, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s
}
