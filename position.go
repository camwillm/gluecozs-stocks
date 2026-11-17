package stocks

import "time"

// position is one lot: a specific purchase of a specific ticker. Gains are
// computed per lot, not per ticker, so buying the same stock twice at two
// different prices tracks correctly.
type position struct {
	ID       int64
	Ticker   string
	Shares   float64
	BuyPrice float64
	BoughtAt time.Time
}
