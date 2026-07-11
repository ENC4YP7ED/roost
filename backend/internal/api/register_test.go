package api

import (
	"net/http"
	"testing"
)

// Public self-registration is gated behind an admin toggle. When disabled it is
// forbidden; when enabled a valid signup creates a non-admin user with a live
// session, and duplicates / weak input are rejected.
func TestSelfRegistration(t *testing.T) {
	h := newHarness(t)

	body := map[string]any{
		"email": "New.User@Example.com", "username": "newbie",
		"first_name": "New", "last_name": "User", "password": "hunter2secret",
	}

	// Disabled by default → 403.
	if res := h.do("POST", "/auth/register", body); res.Code != http.StatusForbidden {
		t.Fatalf("register while disabled = %d, want 403", res.Code)
	}

	h.st.SetSetting("registration:enabled", "1")

	// A valid signup succeeds and returns a session cookie.
	res := h.do("POST", "/auth/register", body)
	if res.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", res.Code, res.Body.String())
	}
	if len(res.Result().Cookies()) == 0 {
		t.Error("registration should set a session cookie")
	}
	u, err := h.st.UserByEmail("new.user@example.com")
	if err != nil {
		t.Fatalf("user not created / email not normalised: %v", err)
	}
	if u.RootAdmin {
		t.Error("self-registered users must never be admins")
	}

	// Duplicate email → 409.
	if res := h.do("POST", "/auth/register", body); res.Code != http.StatusConflict {
		t.Errorf("duplicate register = %d, want 409", res.Code)
	}

	// Weak password → 422.
	weak := map[string]any{"email": "a@b.com", "username": "shorty", "password": "x"}
	if res := h.do("POST", "/auth/register", weak); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("weak password = %d, want 422", res.Code)
	}

	// Missing email → 422.
	noEmail := map[string]any{"email": "not-an-email", "username": "u", "password": "longenough1"}
	if res := h.do("POST", "/auth/register", noEmail); res.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad email = %d, want 422", res.Code)
	}
}
