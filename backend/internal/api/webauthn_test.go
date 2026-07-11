package api

import (
	"net/http"
	"testing"

	"roost/internal/store"
)

// The full authenticator ceremony needs real hardware/a virtual authenticator,
// so here we cover the store layer end-to-end and the HTTP surface around it:
// challenge issuance, listing/rename/delete, and rejection of bogus responses.

func TestWebAuthnStoreCRUD(t *testing.T) {
	h := newHarness(t)
	u := h.mkUser("pk", "pk@example.com", "pkpassword1", false)

	c := &store.WebAuthnCredential{
		UserID: u.ID, Name: "YubiKey", CredentialID: []byte("cred-1"),
		PublicKey: []byte("pub"), Attestation: "none", Transports: `["usb"]`,
		BackupEligible: true,
	}
	if err := h.st.CreateWebAuthnCredential(c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("expected an assigned id")
	}

	got, err := h.st.WebAuthnCredentialByID([]byte("cred-1"))
	if err != nil || got.Name != "YubiKey" {
		t.Fatalf("lookup by credential id: %v", err)
	}

	if err := h.st.TouchWebAuthnCredential([]byte("cred-1"), 7); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _ = h.st.WebAuthnCredentialByID([]byte("cred-1"))
	if got.SignCount != 7 || got.LastUsedAt == nil {
		t.Errorf("touch did not update sign count / last used: %+v", got)
	}

	if err := h.st.RenameWebAuthnCredential(u.ID, c.ID, "Phone"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	list, _ := h.st.WebAuthnCredentials(u.ID)
	if len(list) != 1 || list[0].Name != "Phone" {
		t.Fatalf("rename not reflected: %+v", list)
	}

	// Rename/delete scoped to the owner: another user cannot touch it.
	other := h.mkUser("pk2", "pk2@example.com", "pkpassword2", false)
	if err := h.st.RenameWebAuthnCredential(other.ID, c.ID, "Hijack"); err != store.ErrNotFound {
		t.Errorf("cross-user rename should fail, got %v", err)
	}
	if err := h.st.DeleteWebAuthnCredential(other.ID, c.ID); err != store.ErrNotFound {
		t.Errorf("cross-user delete should fail, got %v", err)
	}
	if err := h.st.DeleteWebAuthnCredential(u.ID, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ := h.st.WebAuthnCredentials(u.ID); len(list) != 0 {
		t.Error("credential not deleted")
	}
}

func TestWebAuthnHTTPSurface(t *testing.T) {
	h := newHarness(t)
	h.mkUser("owner", "owner@example.com", "ownerpass1", false)
	cookie := h.login("owner", "ownerpass1")

	// Listing starts empty.
	if res := h.do("GET", "/api/client/account/passkeys", nil, withCookie(cookie)); res.Code != http.StatusOK {
		t.Fatalf("list = %d", res.Code)
	}

	// Registration begin issues a challenge + credential-creation options.
	begin := h.do("POST", "/api/client/account/passkeys/register/begin", nil, withCookie(cookie))
	if begin.Code != http.StatusOK {
		t.Fatalf("register begin = %d: %s", begin.Code, begin.Body.String())
	}
	if begin.json()["session"] == nil || begin.json()["publicKey"] == nil {
		t.Error("register begin should return a session token and publicKey options")
	}

	// A finish with an unknown session token is rejected.
	bad := h.do("POST", "/api/client/account/passkeys/register/finish",
		map[string]any{"session": "nope", "response": map[string]any{}}, withCookie(cookie))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("finish with bad session = %d, want 400", bad.Code)
	}

	// Public passwordless login begin also issues a discoverable challenge.
	lb := h.do("POST", "/auth/passkey/login/begin", nil)
	if lb.Code != http.StatusOK || lb.json()["session"] == nil {
		t.Fatalf("login begin = %d: %s", lb.Code, lb.Body.String())
	}
	// Finishing with an expired/unknown session fails cleanly.
	lf := h.do("POST", "/auth/passkey/login/finish", map[string]any{"session": "gone", "response": map[string]any{}})
	if lf.Code != http.StatusBadRequest {
		t.Errorf("login finish bad session = %d, want 400", lf.Code)
	}

	// Deleting a non-existent passkey is a 404.
	if res := h.do("DELETE", "/api/client/account/passkeys/999", nil, withCookie(cookie)); res.Code != http.StatusNotFound {
		t.Errorf("delete missing = %d, want 404", res.Code)
	}
}
