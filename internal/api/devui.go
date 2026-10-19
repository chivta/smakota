package api

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed devui.html
var devUI embed.FS

// registerDevUI serves a hand-testing console for the API. It is served by the
// API itself so that it is same-origin: a page loaded from anywhere else would
// need CORS, which the API deliberately does not offer. Registered only outside
// production, since it exists to exercise a local stack.
func (s *Server) registerDevUI(r *gin.Engine) {
	r.GET("/dev", func(c *gin.Context) {
		page, err := devUI.ReadFile("devui.html")
		if err != nil {
			RespondWithError(c, err)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", page)
	})
}
