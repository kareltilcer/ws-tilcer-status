package db

import "embed"

// MigrationsFS holds the platform core's Goose migrations (the Mode B session
// store, version block 02000). The registry merges it into the one boot-time
// migration sequence under the "platform" source.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
