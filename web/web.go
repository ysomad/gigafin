// Package web embeds locales and static assets of the UI.
package web

import "embed"

// FS holds locales/*.json and static/*.
//
//go:embed locales static
var FS embed.FS
