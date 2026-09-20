package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
)

// initDataTTL rejects a Mini App launch payload older than this, so a captured
// initData string is not usable indefinitely.
const initDataTTL = 24 * time.Hour

// auth accepts two callers. The Mini App sends "tma <initData>", which is
// verified against the bot token by Telegram's documented HMAC scheme. The bot
// process sends "Bearer <service token>" plus the Telegram user it acts for.
func (s *Server) auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		telegramID, err := s.identify(c)
		if err != nil {
			RespondWithError(c, err)
			return
		}

		user, err := s.users.GetByTelegramID(c.Request.Context(), telegramID)
		if err != nil {
			RespondWithError(c, err)
			return
		}

		c.Set(userIDKey, user.ID)
		c.Next()
	}
}

// identify verifies the caller and returns the Telegram user it speaks for. It
// stops short of loading the user, so onboarding can authenticate a caller who
// has no row yet.
func (s *Server) identify(c *gin.Context) (int64, error) {
	header := c.GetHeader("Authorization")
	scheme, value, found := strings.Cut(header, " ")
	if !found {
		return 0, domain.ErrUnauthorized
	}

	switch scheme {
	case "tma":
		return verifyInitData(value, s.botToken)
	case "Bearer":
		return s.verifyService(value, c.GetHeader("X-Telegram-User-ID"))
	}
	return 0, domain.ErrUnauthorized
}

// verifyService checks the shared token the bot process uses, in constant time,
// and trusts the user header only once the token matches.
func (s *Server) verifyService(token, userHeader string) (int64, error) {
	match := subtle.ConstantTimeCompare([]byte(token), []byte(s.serviceToken))
	if s.serviceToken == "" || match != 1 {
		return 0, domain.ErrUnauthorized
	}

	id, err := strconv.ParseInt(userHeader, 10, 64)
	if err != nil {
		return 0, domain.ErrUnauthorized
	}
	return id, nil
}

// verifyInitData implements Telegram's Mini App signature check: every field
// except hash, sorted and newline-joined, HMAC'd under a key derived from the
// bot token.
func verifyInitData(raw, botToken string) (int64, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, domain.ErrUnauthorized
	}

	given := values.Get("hash")
	if given == "" {
		return 0, domain.ErrUnauthorized
	}
	values.Del("hash")

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+values.Get(k))
	}

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))

	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	expected := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(expected), []byte(given)) != 1 {
		return 0, domain.ErrUnauthorized
	}

	err = checkFreshness(values.Get("auth_date"))
	if err != nil {
		return 0, err
	}

	return parseInitDataUser(values.Get("user"))
}

func checkFreshness(authDate string) error {
	seconds, err := strconv.ParseInt(authDate, 10, 64)
	if err != nil {
		return domain.ErrUnauthorized
	}
	if time.Since(time.Unix(seconds, 0)) > initDataTTL {
		return domain.ErrUnauthorized
	}
	return nil
}

// parseInitDataUser pulls the id out of the JSON user object without decoding
// fields the API has no use for.
func parseInitDataUser(userJSON string) (int64, error) {
	const marker = `"id":`
	idx := strings.Index(userJSON, marker)
	if idx < 0 {
		return 0, domain.ErrUnauthorized
	}

	rest := strings.TrimLeft(userJSON[idx+len(marker):], " ")
	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		return 0, domain.ErrUnauthorized
	}

	id, err := strconv.ParseInt(strings.TrimSpace(rest[:end]), 10, 64)
	if err != nil {
		return 0, domain.ErrUnauthorized
	}
	return id, nil
}
