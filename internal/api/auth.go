package api

import (
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
)

const bearerPrefix = "Bearer "

// auth accepts one credential: "Authorization: Bearer <token>", where the token
// belongs to exactly one user. The user is whoever owns the token; nothing else
// in the request can change that.
func (s *Server) auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, found := strings.CutPrefix(c.GetHeader("Authorization"), bearerPrefix)
		if !found || token == "" {
			RespondWithError(c, domain.ErrUnauthorized)
			return
		}

		userID, err := s.tokens.Authenticate(c.Request.Context(), domain.HashToken(token))
		if err != nil {
			RespondWithError(c, err)
			return
		}

		c.Set(userIDKey, userID)
		c.Next()
	}
}
