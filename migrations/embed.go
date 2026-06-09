package migrations

import "embed"

// FS holds the SQL migration files compiled into the binary so the single
// `loose-ends` binary can bootstrap a database without the migrations/
// directory present on disk.
//
//go:embed *.sql
var FS embed.FS
