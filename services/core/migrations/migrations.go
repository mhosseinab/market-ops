// Package migrations embeds the goose SQL migrations for the core schema so the
// binary and tests can apply them without depending on files on disk. The goose
// CLI (used by `task db:reset` and the Verify block) reads the same .sql files
// directly from this directory; the embedded FS is the in-process path.
package migrations

import "embed"

// FS holds every goose migration in this directory. Use goose.SetBaseFS(FS)
// with a base directory of "." to apply them from an embedded binary.
//
//go:embed *.sql
var FS embed.FS
