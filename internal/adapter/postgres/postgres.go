// Package postgres keeps the owner state, records, activity log, token usage and daily metric
// snapshots in PostgreSQL. The schema is the same as earlier versions, so an existing database
// is used as-is.
package postgres

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

// Open connects and creates missing tables.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	db := &DB{Pool: pool}
	if err := db.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() { db.Pool.Close() }

var schema = []string{
	`create table if not exists app_state (key text primary key, value jsonb not null, updated_at timestamptz not null default now())`,
	`create table if not exists activity_log (
    id bigserial primary key, at timestamptz not null default now(),
    kind text not null, project_id text, summary text not null, detail jsonb)`,
	`create index if not exists activity_log_at on activity_log (at desc)`,
	`create table if not exists usage_log (
    id bigserial primary key, at timestamptz not null default now(), model text not null,
    input_tokens int not null, output_tokens int not null, cache_read_tokens int not null)`,
	`create table if not exists metrics_daily (
    day date not null, project_id text not null, project_name text not null,
    open int not null, overdue int not null, blocked int not null, stale int not null,
    done_this_week int not null, unassigned int not null, urgent_bugs int not null,
    cycle_name text, cycle_percent int, cycle_total int, cycle_done int,
    metrics jsonb not null, primary key (day, project_id))`,
	`alter table activity_log add column if not exists ws text`,
	`alter table metrics_daily add column if not exists ws text`,
	`create table if not exists pm_records (
    id bigserial primary key, ws text not null, kind text not null, code text, project_id text,
    status text not null default 'open', data jsonb not null, due_at timestamptz,
    created_at timestamptz not null default now(), updated_at timestamptz not null default now())`,
	`create index if not exists pm_records_kind on pm_records (ws, kind, status)`,
}

func (db *DB) Migrate(ctx context.Context) error {
	for _, q := range schema {
		if _, err := db.Pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- state

// StateStore saves the state document. Saves are coalesced: at most one write in flight,
// always writing the newest document.
type StateStore struct {
	db      *DB
	mu      sync.Mutex
	latest  []byte
	running bool
	idle    *sync.Cond
}

func (db *DB) State() *StateStore {
	s := &StateStore{db: db}
	s.idle = sync.NewCond(&s.mu)
	return s
}

func (s *StateStore) Load(ctx context.Context) ([]byte, error) {
	var v []byte
	err := s.db.Pool.QueryRow(ctx, `select value::text from app_state where key = 'state'`).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func (s *StateStore) Save(_ context.Context, doc []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = doc
	if !s.running {
		s.running = true
		go s.writer()
	}
	return nil
}

func (s *StateStore) writer() {
	for {
		s.mu.Lock()
		doc := s.latest
		s.latest = nil
		if doc == nil {
			s.running = false
			s.idle.Broadcast()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		_, err := s.db.Pool.Exec(context.Background(), `insert into app_state (key, value) values ('state', $1::jsonb)
      on conflict (key) do update set value = excluded.value, updated_at = now()`, string(doc))
		if err != nil {
			slog.Error("db: save state failed", "err", err)
		}
	}
}

// Flush waits until queued saves are written.
func (s *StateStore) Flush() {
	s.mu.Lock()
	for s.running {
		s.idle.Wait()
	}
	s.mu.Unlock()
}
