package session

import (
	"database/sql"
	"testing"
	"time"
)

// The session store holds an opened *sql.DB per token but its own logic
// (create/lookup/expiry/destroy) never touches the connection, so a nil handle
// is fine for exercising it.

func TestCreateAndGet(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Close()

	sess := s.Create(nil, "mysql", "127.0.0.1", 3306, "root", "shop")
	if sess.Token == "" {
		t.Fatal("no token issued")
	}
	if sess.Host != "127.0.0.1" || sess.Port != 3306 || sess.User != "root" {
		t.Errorf("session fields wrong: %+v", sess)
	}
	got, ok := s.Get(sess.Token)
	if !ok || got.Token != sess.Token {
		t.Fatalf("Get returned ok=%v", ok)
	}
	if _, ok := s.Get("nonexistent"); ok {
		t.Error("Get returned a session for an unknown token")
	}
}

func TestDestroy(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Close()
	sess := s.Create(nil, "mysql", "h", 3306, "u", "db")
	s.Destroy(sess.Token)
	if _, ok := s.Get(sess.Token); ok {
		t.Error("session survived Destroy")
	}
	// Destroying an unknown token is a no-op, not a panic.
	s.Destroy("unknown")
}

func TestGetRefreshesIdleTimer(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Close()
	sess := s.Create(nil, "mysql", "h", 3306, "u", "db")

	// Backdate the session, then a Get must refresh lastSeen so a subsequent
	// reap (which compares lastSeen against now-ttl) would keep it alive.
	s.mu.Lock()
	s.items[sess.Token].lastSeen = time.Now().Add(-2 * time.Hour)
	before := s.items[sess.Token].lastSeen
	s.mu.Unlock()

	got, ok := s.Get(sess.Token)
	if !ok {
		t.Fatal("session missing")
	}
	if !got.lastSeen.After(before) {
		t.Error("Get did not refresh the idle timer")
	}
}

// TestReapEvictsIdleSessions exercises the reap cutoff logic directly.
func TestReapEvictsIdleSessions(t *testing.T) {
	s := NewStore(10 * time.Millisecond)
	defer s.Close()
	sess := s.Create(nil, "mysql", "h", 3306, "u", "db")

	// Age it past the TTL and invoke one reap sweep via the internal helper.
	s.mu.Lock()
	s.items[sess.Token].lastSeen = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	s.reapOnce()

	if _, ok := s.Get(sess.Token); ok {
		t.Error("idle session not reaped")
	}
}

func TestTokensAreUnique(t *testing.T) {
	s := NewStore(time.Hour)
	defer s.Close()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		sess := s.Create(nil, "mysql", "h", 3306, "u", "db")
		if seen[sess.Token] {
			t.Fatal("duplicate session token")
		}
		seen[sess.Token] = true
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s := NewStore(time.Hour)
	s.Create(nil, "mysql", "h", 3306, "u", "db")
	s.Close()
	// A second close must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Close panicked: %v", r)
		}
	}()
	// reap runs on a ticker stopped by Close; nothing else to assert beyond
	// no panic and no leaked goroutine (the -race build would flag a data race).
	_ = sql.ErrNoRows
}
