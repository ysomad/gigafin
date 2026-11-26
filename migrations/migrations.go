package migrations

import "embed"

// FS holds *.sql.
//
//go:embed *.sql
var FS embed.FS
