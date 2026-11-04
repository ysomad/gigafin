//go:build !apidocs

package api

import "embed"

// DocsEmbedded reports whether DocsFS holds the docs. Scalar is several megabytes, so
// it is left out of the binary unless built with the apidocs tag.
const DocsEmbedded = false

// DocsFS is empty without the apidocs tag.
var DocsFS embed.FS
