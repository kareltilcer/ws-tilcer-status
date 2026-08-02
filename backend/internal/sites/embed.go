package sites

import "embed"

// MigrationsFS holds the sites registry's Goose migrations (version block 10000):
// the whole five-table status schema. The registry merges it under the "sites"
// source; goose applies it after the platform sessions table (02xxx) by numeric
// filename prefix.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
