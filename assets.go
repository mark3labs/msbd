// Package msbd exposes embedded static assets (the OpenAPI spec) for the
// HTTP server's /docs and /openapi.yaml endpoints. Embedding lives at the
// module root because go:embed cannot reference parent directories.
package msbd

import _ "embed"

// OpenAPISpec is the raw bytes of openapi.yaml, the wire contract.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
