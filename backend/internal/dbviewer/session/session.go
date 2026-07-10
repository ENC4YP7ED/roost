// Package session keeps live *sql.DB connection pools alive between HTTP
// requests, keyed by an opaque token handed back to the browser.
package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"sync"
	"time"
)

// Session is one authenticated database connection owned by a browser tab.
type Session struct {
	Token    string
	DB       *sql.DB
	Driver   string // "mysql"
	Host     string
	Port     int
	User     string
	Server   string // human label, e.g. "127.0.0.1:3306"
	Created  time.Time
	lastSeen time.Time
}

// Store is a concurrency-safe registry of sessions with idle expiry.
type Store struct {
	mu       sync.Mutex
	ttl      time.Duration
	items    map[string]*Session
	stopChan chan struct{}
}

// NewStore creates a session store and starts the background reaper.
func NewStore(ttl time.Duration) *Store {
	s := &Store{
		ttl:      ttl,
		items:    make(map[string]*Session),
		stopChan: make(chan struct{}),
	}
	go s.reap()
	return s
}

// Create registers a new session for an already-opened pool.
func (s *Store) Create(db *sql.DB, driver, host string, port int, user, server string) *Session {
	now := time.Now()
	sess := &Session{
		Token:    newToken(),
		DB:       db,
		Driver:   driver,
		Host:     host,
		Port:     port,
		User:     user,
		Server:   server,
		Created:  now,
		lastSeen: now,
	}
	s.mu.Lock()
	s.items[sess.Token] = sess
	s.mu.Unlock()
	return sess
}

// Get returns a session and refreshes its idle timer.
func (s *Store) Get(token string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.items[token]
	if ok {
		sess.lastSeen = time.Now()
	}
	return sess, ok
}

// Destroy closes and removes a session.
func (s *Store) Destroy(token string) {
	s.mu.Lock()
	sess, ok := s.items[token]
	if ok {
		delete(s.items, token)
	}
	s.mu.Unlock()
	if ok && sess.DB != nil {
		_ = sess.DB.Close()
	}
}

// Close tears down the store and every live connection.
func (s *Store) Close() {
	close(s.stopChan)
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, sess := range s.items {
		if sess.DB != nil {
			_ = sess.DB.Close()
		}
		delete(s.items, token)
	}
}

func (s *Store) reap() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-s.ttl)
			s.mu.Lock()
			for token, sess := range s.items {
				if sess.lastSeen.Before(cutoff) {
					if sess.DB != nil {
						_ = sess.DB.Close()
					}
					delete(s.items, token)
				}
			}
			s.mu.Unlock()
		}
	}
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
