// Package stocks is the Gluecozs module that tracks your own stock
// positions: what you paid, what each lot is worth now, and whether it has
// crossed a gain or loss level you care about. It has no connection to any
// brokerage and no order code: positions are entered by hand, and a bug here
// cannot place a trade or move a dollar.
package stocks

import (
	"strconv"
	"strings"
	"time"
)

// Config says how to run the module.
type Config struct {
	// FinnhubAPIKey authenticates quote lookups. Required.
	FinnhubAPIKey string

	// DBPath is where positions and alert state are stored, e.g.
	// "data/stocks.db".
	DBPath string

	// PollInterval is how often open positions are repriced while the
	// market is open.
	PollInterval time.Duration

	// AlwaysPoll ignores market hours and polls on every interval. Useful
	// for testing; leave this off in production so Finnhub isn't hit
	// nights and weekends for no reason.
	AlwaysPoll bool

	// VolatileTickers use the wider "volatile" alert tier (see alerts.go)
	// instead of the default "steady" tier.
	VolatileTickers map[string]bool
}

const (
	defaultInterval = 60 * time.Second
	minInterval     = 10 * time.Second
)

// ConfigFromEnv reads the settings from environment variables:
//
//	GLUECOZS_FINNHUB_API_KEY       Finnhub API key
//	GLUECOZS_STOCKS_DB_PATH        where to store positions (default "data/stocks.db")
//	GLUECOZS_STOCKS_POLL_SECONDS   how often to reprice, in seconds (default 60, minimum 10)
//	GLUECOZS_STOCKS_ALWAYS_POLL    "1" to poll outside market hours too
//	GLUECOZS_STOCKS_VOLATILE       comma-separated tickers that use the wider alert tier
func ConfigFromEnv(get func(string) string) (Config, error) {
	cfg := Config{
		FinnhubAPIKey: strings.TrimSpace(get("GLUECOZS_FINNHUB_API_KEY")),
		DBPath:        strings.TrimSpace(get("GLUECOZS_STOCKS_DB_PATH")),
		PollInterval:  defaultInterval,
		AlwaysPoll:    get("GLUECOZS_STOCKS_ALWAYS_POLL") == "1",
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "data/stocks.db"
	}

	if v := strings.TrimSpace(get("GLUECOZS_STOCKS_POLL_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, errBadPollSeconds(v)
		}
		cfg.PollInterval = time.Duration(n) * time.Second
		if cfg.PollInterval < minInterval {
			cfg.PollInterval = minInterval
		}
	}

	cfg.VolatileTickers = map[string]bool{}
	for _, t := range strings.Split(get("GLUECOZS_STOCKS_VOLATILE"), ",") {
		t = strings.ToUpper(strings.TrimSpace(t))
		if t != "" {
			cfg.VolatileTickers[t] = true
		}
	}
	return cfg, nil
}

type errBadPollSeconds string

func (e errBadPollSeconds) Error() string {
	return "GLUECOZS_STOCKS_POLL_SECONDS: " + string(e) + " is not a number"
}
