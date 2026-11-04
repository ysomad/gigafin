// Package web embeds HTML templates and static assets of the UI.
package web

import "embed"

// FS holds templates/*.html, locales/*.json and static/*.
//
//go:embed templates locales static
var FS embed.FS
