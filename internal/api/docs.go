package api

// docs.go — interactive API documentation. /docs serves a self-contained
// Swagger UI page (assets pulled from a CDN) that renders the embedded
// openapi.yaml, served verbatim at /openapi.yaml.

import "net/http"

// SetOpenAPI registers the raw openapi.yaml bytes served at /openapi.yaml and
// rendered by the Swagger UI at /docs. Pass nil to disable both endpoints.
func (s *Server) SetOpenAPI(spec []byte) *Server {
	s.openapi = spec
	return s
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if len(s.openapi) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(s.openapi)
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerHTML))
}

// swaggerHTML is a minimal Swagger UI host page. It loads the UI bundle from
// the jsDelivr CDN and points it at /openapi.yaml.
const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>msbd — API docs</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
  <style>body { margin: 0; } .topbar { display: none; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-standalone-preset.js" crossorigin></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: "/openapi.yaml",
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
        layout: "StandaloneLayout"
      });
    };
  </script>
</body>
</html>`
