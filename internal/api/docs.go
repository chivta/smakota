package api

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.yaml
var openAPISpec embed.FS

// swaggerUIVersion is pinned so the docs page cannot change under us. Checked
// against the npm registry on 2026-10-18.
const swaggerUIVersion = "5.33.3"

// The spec is written by hand rather than generated from handler annotations.
// Generating would add a swag build step and comment blocks on every handler;
// the cost is that this file and the handlers can drift.
const docsPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>calbot API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@` + swaggerUIVersion + `/swagger-ui.css">
</head>
<body>
  <div id="ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@` + swaggerUIVersion + `/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({ url: "openapi.yaml", dom_id: "#ui", tryItOutEnabled: true });
  </script>
</body>
</html>`

// registerDocs serves the API reference. It sits outside the auth group so the
// page loads without credentials; every documented call still needs them.
func (s *Server) registerDocs(r *gin.Engine) {
	r.GET("/docs", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(docsPage))
	})

	r.GET("/docs/openapi.yaml", func(c *gin.Context) {
		spec, err := openAPISpec.ReadFile("openapi.yaml")
		if err != nil {
			RespondWithError(c, err)
			return
		}
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", spec)
	})
}
