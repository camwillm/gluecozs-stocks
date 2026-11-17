package stocks

import (
	"context"
	"log"
	"sync"
	"time"
)

type cachedQuote struct {
	quote   quote
	fetched time.Time
}

// inflightCall tracks one in-progress fetch so concurrent callers can share it.
type inflightCall struct {
	done  chan struct{}
	quote quote
	err   error
}

// quoteCache fetches quotes from Finnhub and reuses them for a short time,
// so loading the page and the background poller don't double up on
// requests, and so a slow or failing Finnhub doesn't block every reader.
type quoteCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	fetcher  quoteFetcher
	items    map[string]cachedQuote
	inflight map[string]*inflightCall
}

func newQuoteCache(f quoteFetcher, ttl time.Duration) *quoteCache {
	return &quoteCache{
		ttl:      ttl,
		fetcher:  f,
		items:    map[string]cachedQuote{},
		inflight: map[string]*inflightCall{},
	}
}

// fetch gets a fresh quote, sharing one request among concurrent callers.
func (c *quoteCache) fetch(ctx context.Context, ticker string) (quote, error) {
	c.mu.Lock()
	if cl, ok := c.inflight[ticker]; ok {
		c.mu.Unlock()
		select {
		case <-cl.done:
			return cl.quote, cl.err
		case <-ctx.Done():
			return quote{}, ctx.Err()
		}
	}
	cl := &inflightCall{done: make(chan struct{})}
	c.inflight[ticker] = cl
	c.mu.Unlock()

	// WithoutCancel: one request closing mid-fetch shouldn't fail the others
	// sharing it.
	cl.quote, cl.err = c.fetcher.Quote(context.WithoutCancel(ctx), ticker)
	if cl.err != nil {
		log.Printf("stocks: fetch %s: %v", ticker, cl.err)
	}

	c.mu.Lock()
	if cl.err == nil {
		c.items[ticker] = cachedQuote{quote: cl.quote, fetched: time.Now()}
	}
	delete(c.inflight, ticker)
	c.mu.Unlock()
	close(cl.done)
	return cl.quote, cl.err
}

func (c *quoteCache) Get(ctx context.Context, ticker string) (quote, error) {
	c.mu.Lock()
	item, ok := c.items[ticker]
	c.mu.Unlock()

	if ok && time.Since(item.fetched) < c.ttl {
		return item.quote, nil
	}

	q, err := c.fetch(ctx, ticker)
	if err != nil {
		if ok {
			return item.quote, nil // serve stale data instead of failing
		}
		return quote{}, err
	}
	return q, nil
}

// Refresh fetches a fresh quote and stores it, ignoring the TTL.
func (c *quoteCache) Refresh(ctx context.Context, ticker string) error {
	_, err := c.fetch(ctx, ticker)
	return err
}
