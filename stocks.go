package stocks

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // embeds timezone data so it works in minimal Docker images

	"github.com/camwillm/gluecozs-sdk"
)

//go:embed templates/stocks.html
var tplFS embed.FS

var tpl = template.Must(template.ParseFS(tplFS, "templates/stocks.html"))

var nyc = mustLoadLocation("America/New_York")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

var tickerRe = regexp.MustCompile(`^[A-Z.\-]{1,10}$`)

// Module tracks your own stock positions. It is read-only toward your
// brokerage in the strictest sense possible: there is no brokerage
// connection at all. Every lot is entered by hand here, and every trade
// happens wherever you actually trade; this just watches the numbers.
type Module struct {
	cfg    Config
	db     *store
	quotes *quoteCache

	mu          sync.RWMutex
	lastSummary summary
	lastViews   []positionView
	updated     time.Time
	haveData    bool
	recentCount int // alerts raised since the module started

	statesMu sync.Mutex
	states   map[int64]map[string]ruleState

	host sdk.Host
}

// New opens the position store at cfg.DBPath and builds the module. It does
// not poll anything until Run is called.
func New(cfg Config) (*Module, error) {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultInterval
	}
	db, err := openStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	m := &Module{
		cfg:    cfg,
		db:     db,
		quotes: newQuoteCache(newFinnhubClient(cfg.FinnhubAPIKey), cfg.PollInterval*3/2),
	}
	return m, nil
}

func (m *Module) ID() string { return "stocks" }

// Nav is the entry shown in the molecule nav. Its outline turns amber when a
// position has crossed an alert level since the module started.
func (m *Module) Nav() sdk.NavItem {
	n := sdk.NavItem{
		ID:    "stocks",
		Label: "Stocks",
		Href:  "/m/stocks",
		Icon:  `<path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/>`,
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.haveData {
		n.Status = "ok"
		if m.recentCount > 0 {
			n.Status = "warn"
		}
	}
	return n
}

// marketOpen reports whether t falls in 9:30-16:00 ET, Monday to Friday. It
// does not know about market holidays.
func marketOpen(t time.Time) bool {
	t = t.In(nyc)
	switch t.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	mins := t.Hour()*60 + t.Minute()
	return mins >= 9*60+30 && mins < 16*60
}

// Run reprices open positions until ctx is cancelled. Call it in a
// goroutine. It only ever reads quotes; it never places, changes, or
// cancels anything with a broker, because it was never given the means to.
func (m *Module) Run(ctx context.Context) {
	m.loadState(ctx)

	if m.cfg.AlwaysPoll || marketOpen(time.Now()) {
		m.pollOnce(ctx)
	}

	t := time.NewTicker(m.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if m.cfg.AlwaysPoll || marketOpen(now) {
				m.pollOnce(ctx)
			}
		}
	}
}

func (m *Module) loadState(ctx context.Context) {
	s, err := m.db.loadAlertState(ctx)
	if err != nil {
		log.Printf("stocks: load alert state: %v", err)
		return
	}
	m.statesMu.Lock()
	m.states = s
	m.statesMu.Unlock()
}

// loadViews returns every lot priced against its current quote, and the
// total number of lots (which can be more than len(views) if a quote
// couldn't be fetched for one).
func (m *Module) loadViews(ctx context.Context) ([]positionView, int, error) {
	positions, err := m.db.ListPositions(ctx)
	if err != nil {
		return nil, 0, err
	}
	var out []positionView
	for _, p := range positions {
		q, err := m.quotes.Get(ctx, p.Ticker)
		if err != nil {
			log.Printf("stocks: price for %s: %v", p.Ticker, err)
			continue
		}
		out = append(out, computeView(p, q.Price))
	}
	return out, len(positions), nil
}

func (m *Module) pollOnce(ctx context.Context) {
	tickers, err := m.db.Tickers(ctx)
	if err != nil {
		log.Printf("stocks: list tickers: %v", err)
		return
	}
	for _, tk := range tickers {
		if err := m.quotes.Refresh(ctx, tk); err != nil {
			log.Printf("stocks: refresh %s: %v", tk, err)
		}
	}

	views, total, err := m.loadViews(ctx)
	if err != nil {
		log.Printf("stocks: load positions: %v", err)
		return
	}

	m.mu.Lock()
	m.lastViews = views
	m.lastSummary = computeSummary(views, total)
	m.updated = time.Now()
	m.haveData = true
	m.mu.Unlock()

	m.checkAlerts(views)
}

func (m *Module) checkAlerts(views []positionView) {
	m.statesMu.Lock()
	defer m.statesMu.Unlock()
	if m.states == nil {
		m.states = map[int64]map[string]ruleState{}
	}
	now := time.Now()
	fired := 0
	for _, v := range views {
		old := m.states[v.ID]
		alerts, next := evaluate(v, old, tierFor(v.Ticker, m.cfg.VolatileTickers), now)
		m.states[v.ID] = next
		if stateChanged(old, next) {
			if err := m.db.saveAlertState(context.Background(), v.ID, next); err != nil {
				log.Printf("stocks: save alert state: %v", err)
			}
		}
		for _, a := range alerts {
			fired++
			log.Printf("stocks: ALERT [%s] %s", a.Rule, a.Message)
		}
	}
	m.mu.Lock()
	m.recentCount = fired
	m.mu.Unlock()
}

func stateChanged(a, b map[string]ruleState) bool {
	if len(a) != len(b) {
		return true
	}
	for k, x := range b {
		y, ok := a[k]
		if !ok || x.Fired != y.Fired || !x.LastFired.Equal(y.LastFired) {
			return true
		}
	}
	return false
}

// gainBarPercent centers a +/-25% gain range on the middle of the bar, so a
// loss draws short and a gain draws long, with breakeven in the middle.
func gainBarPercent(pct float64) int {
	v := 50 + pct*2
	if v < 2 {
		v = 2
	}
	if v > 100 {
		v = 100
	}
	return int(v)
}

// Widgets is the Focus card shown on the home page.
func (m *Module) Widgets(context.Context) ([]sdk.Widget, error) {
	m.mu.RLock()
	sum, updated, have, recent := m.lastSummary, m.updated, m.haveData, m.recentCount
	m.mu.RUnlock()

	if !have {
		return []sdk.Widget{{Title: "Portfolio", Badge: "Starting"}}, nil
	}
	if sum.Positions == 0 {
		return []sdk.Widget{{Title: "Portfolio", Badge: "No positions yet"}}, nil
	}

	badge := fmt.Sprintf("%+.1f%%", sum.GainPct)
	w := sdk.Widget{Title: "Portfolio", Badge: badge, Warn: sum.GainUSD < 0}
	w.Rows = []sdk.Row{
		{Name: "Value", Value: fmt.Sprintf("$%s", commas(sum.Value)), Percent: 100},
		{
			Name:    "Gain",
			Value:   fmt.Sprintf("%+.1f%% ($%+.2f)", sum.GainPct, sum.GainUSD),
			Percent: gainBarPercent(sum.GainPct),
			Warn:    sum.GainUSD < 0,
		},
	}
	if recent > 0 {
		w.Warn = true
		w.Rows = append(w.Rows, sdk.Row{
			Name: "Alerts", Value: fmt.Sprintf("%d crossed a level", recent), Percent: 100, Warn: true,
		})
	}
	if sum.Priced < sum.Positions {
		w.Rows = append(w.Rows, sdk.Row{
			Name: "Unpriced", Value: fmt.Sprintf("%d of %d lots", sum.Positions-sum.Priced, sum.Positions), Warn: true,
		})
	}
	_ = updated // not shown on the card; the full page has the exact time
	return []sdk.Widget{w}, nil
}

// Mount registers the Stocks page.
func (m *Module) Mount(mux *http.ServeMux, host sdk.Host) {
	m.host = host
	mux.HandleFunc("GET /m/stocks", m.page)
	mux.HandleFunc("POST /m/stocks/refresh", m.refresh)
	mux.HandleFunc("POST /m/stocks/positions", m.addPosition)
	mux.HandleFunc("POST /m/stocks/positions/{id}/delete", m.deletePosition)
}

func (m *Module) refresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	m.pollOnce(ctx)
	http.Redirect(w, r, "/m/stocks", http.StatusSeeOther)
}

// parseBoughtAt turns "2026-03-15" into a time in New York. An empty string
// means "now". Future dates are rejected.
func parseBoughtAt(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now, nil
	}
	d, err := time.ParseInLocation("2006-01-02", s, nyc)
	if err != nil {
		return time.Time{}, errors.New("bought on must look like 2026-03-15")
	}
	if d.After(now) {
		return time.Time{}, errors.New("bought on cannot be in the future")
	}
	return d, nil
}

func (m *Module) addPosition(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ticker := strings.ToUpper(strings.TrimSpace(r.FormValue("ticker")))
	shares, _ := strconv.ParseFloat(r.FormValue("shares"), 64)
	buyPrice, _ := strconv.ParseFloat(r.FormValue("buy_price"), 64)

	m.formError(w, r, func() error {
		if !tickerRe.MatchString(ticker) {
			return errors.New("that doesn't look like a ticker")
		}
		if shares <= 0 || buyPrice <= 0 {
			return errors.New("shares and buy price must be greater than 0")
		}
		boughtAt, err := parseBoughtAt(r.FormValue("bought_at"), time.Now())
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		// Make sure Finnhub knows this ticker before saving it.
		if _, err := m.quotes.Get(ctx, ticker); err != nil {
			return fmt.Errorf("no price data for %s", ticker)
		}
		if _, err := m.db.AddPosition(ctx, position{
			Ticker: ticker, Shares: shares, BuyPrice: buyPrice, BoughtAt: boughtAt,
		}); err != nil {
			log.Printf("stocks: add position: %v", err)
			return errors.New("could not save that position")
		}
		// Reprice right away so the new lot shows a price and gain
		// immediately, instead of waiting for the next poll tick.
		m.pollOnce(ctx)
		return nil
	})
}

func (m *Module) deletePosition(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := m.db.DeletePosition(ctx, id); err != nil && !errors.Is(err, errNotFound) {
		log.Printf("stocks: delete position: %v", err)
	}
	http.Redirect(w, r, "/m/stocks", http.StatusSeeOther)
}

// formError runs action and, on failure, re-renders the page with the error
// shown instead of just bouncing the person back with nothing to go on.
func (m *Module) formError(w http.ResponseWriter, r *http.Request, action func() error) {
	if err := action(); err != nil {
		v := m.view()
		v.FormError = err.Error()
		m.render(w, r, v)
		return
	}
	http.Redirect(w, r, "/m/stocks", http.StatusSeeOther)
}

func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	m.render(w, r, m.view())
}

func (m *Module) render(w http.ResponseWriter, r *http.Request, v view) {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, v); err != nil {
		log.Printf("stocks: render: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	m.host.Page(w, r, http.StatusOK, "stocks", "Stocks", template.HTML(buf.String()))
}

var (
	_ sdk.Module         = (*Module)(nil)
	_ sdk.WidgetProvider = (*Module)(nil)
	_ sdk.Mounter        = (*Module)(nil)
)
