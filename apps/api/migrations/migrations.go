// Package migrations embeds the SQL migration files so the binary is
// self-contained: no migration tool, no mounted directory, no drift between
// the image and the schema it expects.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
