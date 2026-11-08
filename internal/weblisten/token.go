// Package weblisten serves the house bot's audio to browsers: a signed,
// expiring link from /listen opens a player page that streams what the
// bot is playing in Discord, as Ogg Opus.
package weblisten

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// Sign makes a link token for user that stops working at expiry.
func Sign(secret []byte, user string, expiry time.Time) string {
	payload := b64.EncodeToString([]byte(user + "." + strconv.FormatInt(expiry.Unix(), 10)))
	return payload + "." + b64.EncodeToString(mac(secret, payload))
}

// Verify checks a token's signature and expiry and returns its user.
func Verify(secret []byte, tok string, now time.Time) (string, bool) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return "", false
	}
	got, err := b64.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac(secret, payload)) {
		return "", false
	}
	raw, err := b64.DecodeString(payload)
	if err != nil {
		return "", false
	}
	user, exp, ok := strings.Cut(string(raw), ".")
	unix, err := strconv.ParseInt(exp, 10, 64)
	if !ok || err != nil || !now.Before(time.Unix(unix, 0)) {
		return "", false
	}
	return user, true
}

func mac(secret []byte, payload string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return m.Sum(nil)
}
