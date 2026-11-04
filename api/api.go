// Package api embeds the OpenAPI spec of the public API and, in builds with the apidocs
// tag, the Scalar page rendering it.
package api

import "embed"

// FS holds public.yaml.
//
//go:embed public.yaml
var FS embed.FS
