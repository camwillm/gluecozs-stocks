package stocks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// quote is one ticker's latest price.
type quote struct {
	Ticker    string
	Price     float64
	Change    float64
	ChangePct float64
	PrevClose float64
	AsOf      time.Time
}

type quoteFetcher interface {
	Quote(ctx context.Context, ticker string) (quote, error)
}

type finnhubClient struct {
	apiKey string
	http   *http.Client
}

func newFinnhubClient(apiKey string) *finnhubClient {
	return &finnhubClient{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Finnhub's short field names, mapped to ours below.
type finnhubQuote struct {
	C  float64 `json:"c"`  // current price
	D  float64 `json:"d"`  // change
	DP float64 `json:"dp"` // percent change
	PC float64 `json:"pc"` // previous close
	T  int64   `json:"t"`  // unix timestamp
}

func (c *finnhubClient) Quote(ctx context.Context, ticker string) (quote, error) {
	q := url.Values{}
	q.Set("symbol", ticker)
	u := "https://finnhub.io/api/v1/quote?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return quote{}, err
	}
	// Key goes in a header, not the URL, so it can't leak into error messages.
	req.Header.Set("X-Finnhub-Token", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return quote{}, fmt.Errorf("finnhub request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return quote{}, fmt.Errorf("finnhub status %d for %s", resp.StatusCode, ticker)
	}

	var raw finnhubQuote
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return quote{}, fmt.Errorf("decode finnhub response: %w", err)
	}

	// Finnhub returns zeros (not an error) for unknown tickers.
	if raw.C == 0 {
		return quote{}, fmt.Errorf("no price data for %s", ticker)
	}

	return quote{
		Ticker:    ticker,
		Price:     raw.C,
		Change:    raw.D,
		ChangePct: raw.DP,
		PrevClose: raw.PC,
		AsOf:      time.Unix(raw.T, 0),
	}, nil
}
