//go:build apidocs

package api

import "embed"

// DocsEmbedded reports whether DocsFS holds the docs; see nodocs.go.
const DocsEmbedded = true

// DocsFS holds docs/: docs.html and the scripts it loads.
//
//go:embed docs
var DocsFS embed.FS
