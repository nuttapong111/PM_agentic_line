package usecase

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// Store holds the owner state in memory and persists every change.
type Store struct {
	mu       sync.Mutex
	s        *domain.AppState
	persist  port.StatePersister
	defaults domain.PrefDefaults
	now      func() time.Time
}

// NewStore loads the saved state. When the primary store is empty and carry is given (the JSON
// file used before a database was configured), that state is carried over.
func NewStore(ctx context.Context, primary, carry port.StatePersister, d domain.PrefDefaults, now func() time.Time) (*Store, error) {
	st := &Store{persist: primary, defaults: d, now: now}
	st.s = domain.EmptyState(d)
	doc, err := primary.Load(ctx)
	if err != nil {
		return nil, err
	}
	fresh := doc == nil
	if fresh && carry != nil {
		if doc, err = carry.Load(ctx); err != nil {
			slog.Warn("state: could not read the old state file", "err", err)
			doc = nil
		}
	}
	if doc != nil {
		if s, err := domain.UpgradeState(doc, d); err == nil {
			st.s = s
		} else {
			slog.Error("state: saved state unreadable, starting fresh", "err", err)
		}
	}
	if fresh && carry != nil {
		st.save()
	}
	return st, nil
}

func (st *Store) save() {
	st.s.Prune(st.now().UnixMilli())
	b, err := json.Marshal(st.s)
	if err != nil {
		slog.Error("state: marshal failed", "err", err)
		return
	}
	if err := st.persist.Save(context.Background(), b); err != nil {
		slog.Error("state: save failed", "err", err)
	}
}

// Read runs fn with the state locked. fn must not call other Store methods.
func (st *Store) Read(fn func(s *domain.AppState)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(st.s)
}

// Update changes the state and saves it. fn must not call other Store methods.
func (st *Store) Update(fn func(s *domain.AppState)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(st.s)
	st.save()
}

// Reset starts over with an empty state.
func (st *Store) Reset() {
	st.Update(func(s *domain.AppState) { *s = *domain.EmptyState(st.defaults) })
}

// Wipe forgets everything, including connections ("delete all data").
func (st *Store) Wipe() {
	st.Update(func(s *domain.AppState) {
		*s = *domain.EmptyState(st.defaults)
		s.Connections = domain.Connections{Task: domain.TaskConnection{Provider: domain.ProviderNone}}
	})
}

// Snapshot returns a deep copy of the state.
func (st *Store) Snapshot() domain.AppState {
	st.mu.Lock()
	b, _ := json.Marshal(st.s)
	st.mu.Unlock()
	var out domain.AppState
	_ = json.Unmarshal(b, &out)
	return out
}

func (st *Store) Prefs() (p domain.Prefs) {
	st.Read(func(s *domain.AppState) { p = s.Prefs })
	return
}

func (st *Store) Connections() (c domain.Connections) {
	st.Read(func(s *domain.AppState) { c = s.Connections })
	return
}

func (st *Store) CurrentProject() (id string) {
	st.Read(func(s *domain.AppState) { id = s.Current() })
	return
}

func (st *Store) Nicknames() (n map[string]string) {
	st.Read(func(s *domain.AppState) { n = s.Prefs.Nicknames })
	return
}

// Once marks a dedupe key as sent; it reports false when it was sent before.
func (st *Store) Once(key string) (first bool) {
	st.Update(func(s *domain.AppState) {
		if _, ok := s.Sent[key]; ok {
			return
		}
		s.Sent[key] = st.now().UnixMilli()
		first = true
	})
	return
}
