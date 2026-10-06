package migrations

import "embed"

// Files contains ordered SQL schema migrations shipped with the API binary.
//
//go:embed *.sql
var Files embed.FS
