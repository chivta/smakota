package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/bcrypt"
)

const bearerPrefix = "Bearer "

const tokenIDKey = "token_id"

// bcrypt silently ignores everything past this many bytes.
const maxPasswordBytes = 72

// Labels of the tokens that register and login mint.
const (
	tokenNameRegister = "register"
	tokenNameLogin    = "login"
)

// maxBytesTag bounds a string by bytes, where validator's max counts runes.
const maxBytesTag = "maxbytes"

func init() {
	err := validate.RegisterValidation(maxBytesTag, func(fl validator.FieldLevel) bool {
		return len(fl.Field().String()) <= maxPasswordBytes
	})
	if err != nil {
		panic(err)
	}
}

// dummyHash gives an unknown-email login the same bcrypt cost as a wrong
// password, so response time does not reveal which one it was.
var dummyHash = mustHash("dummy-password-for-timing")

func mustHash(password string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}

// auth accepts one credential: "Authorization: Bearer <token>", either an opaque
// API token or a JWT from the identity provider, each belonging to exactly one
// local user. The user is whoever owns the token; nothing else
// in the request can change that.
func (s *Server) auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, found := strings.CutPrefix(c.GetHeader("Authorization"), bearerPrefix)
		if !found || token == "" {
			RespondWithError(c, domain.ErrUnauthorized)
			return
		}

		if isJWT(token) {
			userID, err := s.authenticateJWT(c, token)
			if err != nil {
				RespondWithError(c, err)
				return
			}
			c.Set(userIDKey, userID)
			c.Next()
			return
		}

		userID, tokenID, err := s.tokens.Authenticate(c.Request.Context(), domain.HashToken(token))
		if err != nil {
			RespondWithError(c, err)
			return
		}

		c.Set(userIDKey, userID)
		c.Set(tokenIDKey, tokenID)
		c.Next()
	}
}

// currentToken pulls the id of the token that authenticated the request.
func currentToken(c *gin.Context) (int64, error) {
	v, ok := c.Get(tokenIDKey)
	if !ok {
		return 0, domain.ErrUnauthorized
	}
	id, ok := v.(int64)
	if !ok {
		return 0, domain.ErrUnauthorized
	}
	return id, nil
}

// emailField normalizes on decode, so validation sees the form that is stored.
type emailField string

func (e *emailField) UnmarshalText(text []byte) error {
	*e = emailField(domain.NormalizeEmail(string(text)))
	return nil
}

type registerRequest struct {
	Email    emailField `json:"email"    validate:"required,email,max=254"`
	Password string     `json:"password" validate:"required,min=8,maxbytes"`
}

type loginRequest struct {
	Email    emailField `json:"email"    validate:"required,email,max=254"`
	Password string     `json:"password" validate:"required,maxbytes"`
}

type sessionResponse struct {
	Token string      `json:"token"`
	User  domain.User `json:"user"`
}

func (s *Server) register(c *gin.Context) {
	var req registerRequest
	err := bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	user, err := s.users.CreateWithCredentials(c.Request.Context(), string(req.Email), string(hash))
	if err != nil {
		RespondWithError(c, err)
		return
	}

	token, err := s.mintToken(c, user.ID, tokenNameRegister)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusCreated, sessionResponse{Token: token, User: user})
}

func (s *Server) login(c *gin.Context) {
	var req loginRequest
	err := bindAndValidate(c, &req)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	userID, hash, err := s.users.GetCredentials(c.Request.Context(), string(req.Email))
	if errors.Is(err, domain.ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		RespondWithError(c, domain.ErrUnauthorized)
		return
	}
	if err != nil {
		RespondWithError(c, err)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password))
	if err != nil {
		RespondWithError(c, domain.ErrUnauthorized)
		return
	}

	user, err := s.users.GetByID(c.Request.Context(), userID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	token, err := s.mintToken(c, userID, tokenNameLogin)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	c.JSON(http.StatusOK, sessionResponse{Token: token, User: user})
}

// logout revokes the token that authenticated the call. Revoking is idempotent,
// and a revoked token cannot reach this handler again anyway. A JWT caller has
// no local token: the identity provider owns that session, so this is a no-op.
func (s *Server) logout(c *gin.Context) {
	if _, isOpaque := c.Get(tokenIDKey); !isOpaque {
		c.Status(http.StatusNoContent)
		return
	}

	tokenID, err := currentToken(c)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	err = s.tokens.Revoke(c.Request.Context(), tokenID)
	if err != nil {
		RespondWithError(c, err)
		return
	}

	log.Info().Int64("token_id", tokenID).Msg("token revoked by logout")
	c.Status(http.StatusNoContent)
}

func (s *Server) mintToken(c *gin.Context, userID int64, name string) (string, error) {
	token, err := domain.NewToken()
	if err != nil {
		return "", err
	}

	_, err = s.tokens.Issue(c.Request.Context(), userID, name, domain.HashToken(token))
	if err != nil {
		return "", err
	}
	return token, nil
}
