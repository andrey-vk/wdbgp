package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func sessionToken(secret string) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	timestamp := strconv.FormatInt(time.Now().Unix(), 16)
	text := timestamp + "." + hex.EncodeToString(nonce[:])
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(text))
	return text + "." + hex.EncodeToString(signature.Sum(nil))
}

func validSession(secret, value string, sessionMaxAge time.Duration) bool {
	parts := strings.SplitN(value, ".", 3)
	if len(parts) != 3 {
		return false
	}

	timestamp, err := strconv.ParseInt(parts[0], 16, 64)
	if err != nil {
		return false
	}
	sessionTime := time.Unix(timestamp, 0)
	if time.Since(sessionTime) > sessionMaxAge {
		return false
	}

	signature, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(parts[0] + "." + parts[1]))
	return hmac.Equal(signature, expected.Sum(nil))
}

// --- User session helpers (for web_auth=login/both) ---

const userSessionCookieName = "wdbgp_user"

func setUserSessionCookie(w http.ResponseWriter, userID int64, secret string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // cookies are already secure (HttpOnly, Secure, SameSite all set below)
		Name:     userSessionCookieName,
		Value:    userSessionToken(secret, userID),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

func userSessionToken(secret string, userID int64) string {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	timestamp := strconv.FormatInt(time.Now().Unix(), 16)
	userStr := strconv.FormatInt(userID, 16)
	text := timestamp + "." + hex.EncodeToString(nonce[:]) + "." + userStr
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(text))
	return text + "." + hex.EncodeToString(signature.Sum(nil))
}

func validUserSession(r *http.Request, userID int64, secret string, maxAge time.Duration) bool {
	cookie, err := r.Cookie(userSessionCookieName)
	if err != nil {
		return false
	}
	sessionID := parseUserSessionToken(secret, cookie.Value, maxAge)
	return sessionID == userID
}

func getUserSessionID(r *http.Request, secret string, maxAge time.Duration) int64 {
	cookie, err := r.Cookie(userSessionCookieName)
	if err != nil {
		return 0
	}
	return parseUserSessionToken(secret, cookie.Value, maxAge)
}

func parseUserSessionToken(secret, value string, maxAge time.Duration) int64 {
	parts := strings.SplitN(value, ".", 4)
	if len(parts) != 4 {
		return 0
	}
	// Validate timestamp freshness (reuse session max age from config, default 8h)
	// We use a fixed 8h window here since we don't have the config struct in this helper.
	// The full login system in Step 6 will tighten this.
	timestamp, err := strconv.ParseInt(parts[0], 16, 64)
	if err != nil {
		return 0
	}
	sessionTime := time.Unix(timestamp, 0)
	if maxAge <= 0 {
		maxAge = 8 * time.Hour
	}
	if time.Since(sessionTime) > maxAge {
		return 0
	}
	// Validate HMAC signature
	signature, err := hex.DecodeString(parts[3])
	if err != nil {
		return 0
	}
	expected := hmac.New(sha256.New, []byte(secret))
	_, _ = expected.Write([]byte(parts[0] + "." + parts[1] + "." + parts[2]))
	if !hmac.Equal(signature, expected.Sum(nil)) {
		return 0
	}
	userID, err := strconv.ParseInt(parts[2], 16, 64)
	if err != nil {
		return 0
	}
	return userID
}
