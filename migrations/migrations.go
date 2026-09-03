package migrations

import "embed"

// Files contains the database migrations.
//
//go:embed *.sql
var Files embed.FS
