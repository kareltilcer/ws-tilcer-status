package notify

import "embed"

// MigrationsFS holds the notify module's Goose migrations (version block 30000).
// bootstrap merges it after the platform sessions (02xxx), the sites schema
// (10xxx) and feedback (20xxx) — its tables FK into site and crash_group.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
