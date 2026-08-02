// Package bootstrap composes the compile-time modular monolith. It is the single
// place that assembles the one Goose migration sequence from each source's
// per-package files. Both the server entrypoint (cmd/status) and tests go through
// here so they always agree on the schema.
//
// bootstrap sits above the modules (it imports them); modules never import
// bootstrap, so there is no cycle. It stays out of platform/ precisely because
// platform must not depend on modules.
package bootstrap

import (
	"io/fs"

	appdb "github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/db"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/platform/registry"
	"github.com/kareltilcer/ws-tilcer-status/backend/internal/sites"
)

// MigrationSources returns every migration contributor. Goose applies migrations
// globally by their numeric filename prefix, so the effective order is
// platform(02) → sites(10). The sessions table (02xxx) is created before the
// sites schema (10xxx). Because every child table FKs to `site`, the whole
// five-table schema lives in one migration owned by the sites package (the join
// point); monitoring/crash/retention own no tables.
func MigrationSources() []registry.MigrationSource {
	return []registry.MigrationSource{
		{Name: "platform", FS: appdb.MigrationsFS},
		{Name: "sites", FS: sites.MigrationsFS},
	}
}

// MigrationFS assembles the merged, boot-time migration FS for the whole service.
func MigrationFS() (fs.FS, error) {
	return registry.MergeMigrations(MigrationSources())
}
