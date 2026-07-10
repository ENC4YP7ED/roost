package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("password stored in plaintext")
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Error("correct password rejected")
	}
	if CheckPassword(hash, "wrong password") {
		t.Error("wrong password accepted")
	}
	if CheckPassword("not-a-bcrypt-hash", "anything") {
		t.Error("malformed hash accepted a password")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("identical passwords produced identical hashes; salt missing")
	}
}

func TestRandomAlnumShapeAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := RandomAlnum(32)
		if len(s) != 32 {
			t.Fatalf("length = %d, want 32", len(s))
		}
		for _, r := range s {
			if !strings.ContainsRune(alnum, r) {
				t.Fatalf("character %q outside the alphabet", r)
			}
		}
		if seen[s] {
			t.Fatal("RandomAlnum repeated a value")
		}
		seen[s] = true
	}
}

func TestUUIDIsVersion4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		u := UUID()
		if len(u) != 36 {
			t.Fatalf("UUID %q has length %d, want 36", u, len(u))
		}
		parts := strings.Split(u, "-")
		if len(parts) != 5 {
			t.Fatalf("UUID %q does not have 5 groups", u)
		}
		if parts[2][0] != '4' {
			t.Errorf("UUID %q is not version 4", u)
		}
		// Variant bits: first nibble of group 4 must be 8, 9, a or b.
		if !strings.ContainsRune("89ab", rune(parts[3][0])) {
			t.Errorf("UUID %q has wrong variant nibble %q", u, parts[3][0])
		}
		if seen[u] {
			t.Fatal("UUID repeated")
		}
		seen[u] = true
	}
}

func TestSHA256HexIsStable(t *testing.T) {
	// Known vector so a change of algorithm can never silently invalidate
	// every stored session and API token.
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := SHA256Hex("hello"); got != want {
		t.Errorf("SHA256Hex(hello) = %s, want %s", got, want)
	}
	if SHA256Hex("a") == SHA256Hex("b") {
		t.Error("distinct inputs collided")
	}
}

func TestRandomHexLength(t *testing.T) {
	if got := len(RandomHex(16)); got != 32 {
		t.Errorf("RandomHex(16) produced %d chars, want 32", got)
	}
}

// ---- TOTP ----

func TestVerifyTOTPAcceptsCurrentCode(t *testing.T) {
	secret := NewTOTPSecret()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("secret is not valid base32: %v", err)
	}
	code := totpCode(key, time.Now().Unix()/30)
	if !VerifyTOTP(secret, code) {
		t.Errorf("current code %q rejected", code)
	}
}

func TestVerifyTOTPAllowsOnePeriodOfDrift(t *testing.T) {
	secret := NewTOTPSecret()
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	counter := time.Now().Unix() / 30

	for _, drift := range []int64{-1, 0, 1} {
		if code := totpCode(key, counter+drift); !VerifyTOTP(secret, code) {
			t.Errorf("code for drift %d rejected", drift)
		}
	}
	// Two periods out must not be accepted.
	for _, drift := range []int64{-2, 2} {
		if code := totpCode(key, counter+drift); VerifyTOTP(secret, code) {
			t.Errorf("code for drift %d accepted; window too wide", drift)
		}
	}
}

func TestVerifyTOTPRejectsGarbage(t *testing.T) {
	secret := NewTOTPSecret()
	cases := []string{"", "12345", "1234567", "abcdef", "000000 "}
	for _, code := range cases {
		if VerifyTOTP(secret, code) {
			t.Errorf("garbage code %q accepted", code)
		}
	}
	if VerifyTOTP("!!!not-base32!!!", "123456") {
		t.Error("invalid secret accepted")
	}
}

func TestTOTPCodeIsSixDigits(t *testing.T) {
	key := []byte("12345678901234567890")
	code := totpCode(key, 1)
	if len(code) != 6 {
		t.Fatalf("code %q is not 6 characters", code)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code %q contains a non-digit", code)
		}
	}
}

// RFC 6238 test vectors (SHA-1, 8 digits truncated to our 6).
func TestTOTPMatchesRFC6238(t *testing.T) {
	key := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
	}
	for _, tc := range cases {
		if got := totpCode(key, tc.unix/30); got != tc.want {
			t.Errorf("totpCode at t=%d = %s, want %s", tc.unix, got, tc.want)
		}
	}
}

func TestTOTPUri(t *testing.T) {
	uri := TOTPUri("Roost", "admin@example.com", "SECRET")
	for _, want := range []string{"otpauth://totp/", "Roost", "admin@example.com", "secret=SECRET", "issuer=Roost"} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI %q missing %q", uri, want)
		}
	}
}

func TestNewTOTPSecretIsDecodableAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s := NewTOTPSecret()
		if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s); err != nil {
			t.Fatalf("secret %q not decodable: %v", s, err)
		}
		if seen[s] {
			t.Fatal("secret repeated")
		}
		seen[s] = true
	}
}

// ---- JWT ----

func TestSignJWTStructureAndSignature(t *testing.T) {
	const key = "daemon-token"
	token, err := SignJWT(key, map[string]any{"sub": "abc", "n": 1})
	if err != nil {
		t.Fatalf("SignJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}

	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header not base64url: %v", err)
	}
	var h map[string]string
	if err := json.Unmarshal(header, &h); err != nil {
		t.Fatalf("header not JSON: %v", err)
	}
	if h["alg"] != "HS256" || h["typ"] != "JWT" {
		t.Errorf("unexpected header %v", h)
	}

	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("payload not base64url: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if claims["sub"] != "abc" {
		t.Errorf("claim sub = %v", claims["sub"])
	}

	// Recompute the signature the way wings would.
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Error("signature does not verify with the signing key")
	}

	// A different key must not validate.
	mac2 := hmac.New(sha256.New, []byte("other-key"))
	mac2.Write([]byte(parts[0] + "." + parts[1]))
	if parts[2] == base64.RawURLEncoding.EncodeToString(mac2.Sum(nil)) {
		t.Error("signature validated under the wrong key")
	}
}

func TestStandardClaims(t *testing.T) {
	claims := StandardClaims("https://panel", "https://node", 10*time.Minute)
	for _, k := range []string{"iss", "aud", "jti", "iat", "nbf", "exp"} {
		if _, ok := claims[k]; !ok {
			t.Errorf("missing registered claim %q", k)
		}
	}
	if claims["iss"] != "https://panel" {
		t.Errorf("iss = %v", claims["iss"])
	}
	exp := claims["exp"].(int64)
	iat := claims["iat"].(int64)
	if exp-iat < int64((10 * time.Minute).Seconds()) {
		t.Errorf("exp (%d) is not ~10m after iat (%d)", exp, iat)
	}
	if nbf := claims["nbf"].(int64); nbf >= iat {
		t.Error("nbf should precede iat to tolerate clock skew")
	}
	// jti must be unique across tokens (replay protection).
	other := StandardClaims("https://panel", "https://node", time.Minute)
	if claims["jti"] == other["jti"] {
		t.Error("jti repeated across tokens")
	}
}
