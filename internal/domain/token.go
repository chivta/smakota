package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the entropy of an API token.
const tokenBytes = 32

// NewToken returns a fresh API token. Only its hash is ever stored, so this is
// the one moment the plaintext exists.
func NewToken() (string, error) {
	raw := make([]byte, tokenBytes)
	_, err := rand.Read(raw)
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// HashToken is the form a token is stored and looked up by.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
