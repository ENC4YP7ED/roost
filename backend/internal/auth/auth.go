// Package auth bundles the panel's cryptographic primitives: bcrypt password
// hashing, random identifiers, TOTP (RFC 6238), and the HS256 JWTs wings
// verifies for websocket/upload access.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), 10)
	return string(b), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomAlnum returns n random characters from [a-zA-Z0-9], matching the
// style of Pterodactyl's Str::random().
func RandomAlnum(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	for i, b := range buf {
		buf[i] = alnum[int(b)%len(alnum)]
	}
	return string(buf)
}

func RandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

// UUID returns a random RFC 4122 version 4 UUID.
func UUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// SHA256Hex is used to store API tokens and session tokens at rest.
func SHA256Hex(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---- TOTP ----

// NewTOTPSecret generates a 160-bit base32 secret.
func NewTOTPSecret() string {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
}

// VerifyTOTP checks a 6-digit code against the secret, allowing one period
// of clock drift in each direction.
func VerifyTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}
	counter := time.Now().Unix() / 30
	for _, c := range []int64{counter - 1, counter, counter + 1} {
		if totpCode(key, c) == code {
			return true
		}
	}
	return false
}

func totpCode(key []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	val := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", val%1000000)
}

// TOTPUri renders the otpauth:// URI encoded into provisioning QR codes.
func TOTPUri(issuer, account, secret string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s", issuer, account, secret, issuer)
}

// ---- JWT (HS256), compatible with what wings validates ----

func b64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// SignJWT builds an HS256 JWT over the claims map using the given key.
func SignJWT(key string, claims map[string]any) (string, error) {
	header := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := header + "." + b64url(body)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(signing))
	return signing + "." + b64url(mac.Sum(nil)), nil
}

// StandardClaims returns the registered claims Pterodactyl includes on every
// wings-bound token.
func StandardClaims(issuer, audience string, ttl time.Duration) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": issuer,
		"aud": []string{audience},
		"jti": RandomHex(16),
		"iat": now.Unix(),
		"nbf": now.Add(-5 * time.Minute).Unix(),
		"exp": now.Add(ttl).Unix(),
	}
}
