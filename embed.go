// Package toko exposes assets that must travel with the compiled binaries.
package toko

import "embed"

// Migrations carries the SQL migration files. Embedding them means the image
// cannot drift from the schema it expects: the runtime image ships no SQL
// files, so a migration binary built from this commit always applies exactly
// the migrations present in this commit.
//
//go:embed migrations/*.sql
var Migrations embed.FS
