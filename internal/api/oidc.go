package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// jwtSeparator tells the credential shapes apart: a JWT always contains dots,
// and the opaque tokens domain.NewToken makes never do.
const jwtSeparator = "."

// isJWT reports whether a bearer credential takes the JWT path.
func isJWT(token string) bool {
	return strings.Contains(token, jwtSeparator)
}

// idTokenVerifier is the part of *oidc.IDTokenVerifier the middleware uses.
type idTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken string) (*oidc.IDToken, error)
}

// newOIDCVerifier fetches the provider metadata once; the verifier's remote key
// set then caches the JWKS and refetches it on an unknown key id.
func newOIDCVerifier(ctx context.Context, issuer, audience string) (idTokenVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("fetch oidc provider %s: %w", issuer, err)
	}
	return provider.VerifierContext(ctx, &oidc.Config{ClientID: audience}), nil
}

// authenticateJWT verifies the token and maps its subject to a local user,
// creating the user on first sight.
func (s *Server) authenticateJWT(c *gin.Context, token string) (int64, error) {
	if s.idTokens == nil {
		return 0, domain.ErrUnauthorized
	}

	ctx := c.Request.Context()
	idToken, err := s.idTokens.Verify(ctx, token)
	if err != nil {
		log.Debug().Err(err).Msg("jwt rejected")
		return 0, domain.ErrUnauthorized
	}

	var claims struct {
		Email string `json:"email"`
	}
	err = idToken.Claims(&claims)
	if err != nil {
		return 0, domain.ErrUnauthorized
	}

	return s.users.GetOrCreateBySubject(ctx, idToken.Subject, domain.NormalizeEmail(claims.Email))
}
