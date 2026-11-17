package stocks

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// errNotFound is returned by DeletePosition for an id that doesn't exist (or
// belongs to someone else).
var errNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS positions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  ticker     TEXT    NOT NULL,
  shares     REAL    NOT NULL,
  buy_price  REAL    NOT NULL,
  bought_at  TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS alert_state (
  position_id INTEGER NOT NULL,
  rule        TEXT    NOT NULL,
  fired       INTEGER NOT NULL,
  last_fired  TEXT    NOT NULL,
  PRIMARY KEY (position_id, rule)
);
`

// store is a self-hosted, single-user position ledger. It never talks to a
// brokerage: every row is entered by hand, and reading it back can't place
// or change a trade.
type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite allows one writer at a time
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) AddPosition(ctx context.Context, p position) (position, error) {
	p.Ticker = strings.ToUpper(strings.TrimSpace(p.Ticker))
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO positions (ticker, shares, buy_price, bought_at)
		 VALUES (?, ?, ?, ?)`,
		p.Ticker, p.Shares, p.BuyPrice, p.BoughtAt.Format(time.RFC3339))
	if err != nil {
		return position{}, err
	}
	p.ID, err = res.LastInsertId()
	return p, err
}

func (s *store) ListPositions(ctx context.Context) ([]position, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ticker, shares, buy_price, bought_at FROM positions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []position
	for rows.Next() {
		var p position
		var boughtAt string
		if err := rows.Scan(&p.ID, &p.Ticker, &p.Shares, &p.BuyPrice, &boughtAt); err != nil {
			return nil, err
		}
		p.BoughtAt, err = time.Parse(time.RFC3339, boughtAt)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *store) DeletePosition(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM positions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotFound
	}
	// Clean up this lot's alert state so a deleted lot leaves nothing behind.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM alert_state WHERE position_id = ?`, id); err != nil {
		log.Printf("stocks: delete alert state for position %d: %v", id, err)
	}
	return nil
}

func (s *store) Tickers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ticker FROM positions ORDER BY ticker`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *store) loadAlertState(ctx context.Context) (map[int64]map[string]ruleState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT position_id, rule, fired, last_fired FROM alert_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]map[string]ruleState{}
	for rows.Next() {
		var id int64
		var rule, last string
		var fired int
		if err := rows.Scan(&id, &rule, &fired, &last); err != nil {
			return nil, err
		}
		st := ruleState{Fired: fired == 1}
		if last != "" {
			t, err := time.Parse(time.RFC3339, last)
			if err != nil {
				return nil, err
			}
			st.LastFired = t
		}
		if out[id] == nil {
			out[id] = map[string]ruleState{}
		}
		out[id][rule] = st
	}
	return out, rows.Err()
}

func (s *store) saveAlertState(ctx context.Context, positionID int64, state map[string]ruleState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for rule, st := range state {
		fired := 0
		if st.Fired {
			fired = 1
		}
		last := ""
		if !st.LastFired.IsZero() {
			last = st.LastFired.Format(time.RFC3339)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO alert_state (position_id, rule, fired, last_fired)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(position_id, rule)
			 DO UPDATE SET fired = excluded.fired, last_fired = excluded.last_fired`,
			positionID, rule, fired, last); err != nil {
			return err
		}
	}
	return tx.Commit()
}
