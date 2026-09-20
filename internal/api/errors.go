package api

import (
	"errors"
	"net/http"

	"github.com/arvlas/calbot/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// errorBody is the only error shape the API emits. It carries a machine-readable
// code; the human message lives in the client's translation map, keyed by it.
type errorBody struct {
	Code string `json:"code"`
}

// RespondWithError is the single exit for every failed request. Handlers never
// format error JSON by hand.
func RespondWithError(c *gin.Context, err error) {
	var appErr *domain.AppError
	if errors.As(err, &appErr) {
		c.AbortWithStatusJSON(appErr.Status, errorBody{Code: appErr.Code})
		return
	}

	log.Error().Err(err).Str("path", c.FullPath()).Msg("unhandled request error")
	c.AbortWithStatusJSON(http.StatusInternalServerError, errorBody{Code: domain.ErrInternal.Code})
}
