package feedback

import "embed"

// MigrationsFS holds the feedback module's Goose migrations (version block
// 20000): the four tables of PRD §V3-5. bootstrap merges it after the platform
// sessions (02xxx) and the sites schema (10xxx); goose applies the merged
// sequence purely by numeric filename prefix.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
