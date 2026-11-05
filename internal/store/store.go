// Package store keeps the latest snapshot produced by each collector.
// The API only reads from here; collectors write from their own goroutines.
package store

import (
	"sync"
	"time"
)

type Entry struct {
	Name        string     `json:"name"`
	Enabled     bool       `json:"enabled"`
	Interval    int        `json:"interval_seconds"`
	UpdatedAt   *time.Time `json:"updated_at"`   // last successful collection
	LastAttempt *time.Time `json:"last_attempt"` // last collection, successful or not
	DurationMs  int64      `json:"duration_ms"`
	Error       string     `json:"error,omitempty"`
	Data        any        `json:"data"`
}

type Store struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

func New() *Store {
	return &Store{entries: make(map[string]*Entry)}
}

// Register declares a collector so it appears in the API before its first run.
func (s *Store) Register(name string, enabled bool, interval int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[name] = &Entry{Name: name, Enabled: enabled, Interval: interval}
}

// Update records a collection result. On error the previous data is kept
// so the UI can still show the last known state.
func (s *Store) Update(name string, data any, err error, took time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[name]
	if !ok {
		e = &Entry{Name: name, Enabled: true}
		s.entries[name] = e
	}
	now := time.Now()
	e.LastAttempt = &now
	e.DurationMs = took.Milliseconds()
	if err != nil {
		e.Error = err.Error()
		if data != nil {
			e.Data = data
		}
		return
	}
	e.Error = ""
	e.Data = data
	e.UpdatedAt = &now
}

// Get returns a copy of the entry.
func (s *Store) Get(name string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[name]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// All returns a copy of every entry.
func (s *Store) All() map[string]Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Entry, len(s.entries))
	for k, e := range s.entries {
		out[k] = *e
	}
	return out
}
