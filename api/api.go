// Package api embeds the OpenAPI spec of the gigafin API and the Scalar page rendering it.
package api

import "embed"

// FS holds openapi.yaml.
//
//go:embed openapi.yaml
var FS embed.FS

// DocsFS holds docs/: docs.html and the scripts it loads.
//
//go:embed docs
var DocsFS embed.FS
