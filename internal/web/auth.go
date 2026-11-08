package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

// tgUser is the subset of the Telegram WebApp user we need.
type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

var errBadInitData = errors.New("invalid initData")

// validateInitData verifies the Telegram WebApp initData signature against the
// bot token and returns the embedded user. See:
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func validateInitData(initData, botToken string) (*tgUser, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return nil, errBadInitData
	}
	hash := values.Get("hash")
	if hash == "" {
		return nil, errBadInitData
	}
	values.Del("hash")

	// Build the data-check-string: "key=value" pairs sorted by key, \n-joined.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(values.Get(k))
	}

	// secret = HMAC_SHA256(key="WebAppData", msg=botToken)
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	computed := hmacSHA256(secret, []byte(b.String()))
	if !hmac.Equal([]byte(hex.EncodeToString(computed)), []byte(hash)) {
		return nil, errBadInitData
	}

	var u tgUser
	if err := json.Unmarshal([]byte(values.Get("user")), &u); err != nil {
		return nil, errBadInitData
	}
	if u.ID == 0 {
		return nil, errBadInitData
	}
	return &u, nil
}

func hmacSHA256(key, msg []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(msg)
	return h.Sum(nil)
}
