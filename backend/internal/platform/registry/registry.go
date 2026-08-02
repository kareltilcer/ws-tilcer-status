// Package registry is the core of the compile-time modular monolith. It defines
// the Module contract every feature registers through — routes and migrations —
// and the composition helpers the core uses to build the router and assemble the
// one Goose migration sequence.
//
// registry lives in platform/ and is imported BY modules; it must never import a
// module.
package registry

import (
	"fmt"
	"io/fs"
	"path"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

// Module is what every feature module implements to plug into the core. One
// binary, one deploy; modules are compiled in (no runtime plugins).
type Module interface {
	// Name is the code identifier: "sites" | "monitoring" | "crash".
	Name() string
	// RegisterRoutes mounts the module's routes on the authenticated /api router.
	// The module applies its own role gates (RequireWrite / RequireAdmin).
	RegisterRoutes(r chi.Router)
	// Migrations returns this module's Goose *.sql files (embedded, under a
	// "migrations/" prefix). May be nil for a module that owns no tables.
	Migrations() fs.FS
}

// MigrationSource is one contributor of Goose *.sql files: a module (or the
// platform core). Files are embedded under a "migrations/" prefix and carry a
// globally-unique version-number prefix so the merged sequence is deterministic.
type MigrationSource struct {
	Name string
	FS   fs.FS
}

// MergeMigrations flattens every source's migrations/*.sql into a single FS whose
// files goose applies in one ascending sequence. Filenames must be globally
// unique (the version-block convention guarantees this); a collision fails fast.
func MergeMigrations(sources []MigrationSource) (fs.FS, error) {
	merged := fstest.MapFS{}
	for _, src := range sources {
		if src.FS == nil {
			continue
		}
		files, err := fs.Glob(src.FS, "migrations/*.sql")
		if err != nil {
			return nil, fmt.Errorf("registry: glob %s migrations: %w", src.Name, err)
		}
		for _, f := range files {
			name := path.Base(f)
			if _, dup := merged[name]; dup {
				return nil, fmt.Errorf("registry: duplicate migration filename %q (source %s)", name, src.Name)
			}
			b, err := fs.ReadFile(src.FS, f)
			if err != nil {
				return nil, fmt.Errorf("registry: read %s: %w", f, err)
			}
			merged[name] = &fstest.MapFile{Data: b}
		}
	}
	if len(merged) == 0 {
		return nil, fmt.Errorf("registry: no migrations found across %d sources", len(sources))
	}
	return merged, nil
}

// MountAll registers every module's routes on the /api router in the given order.
func MountAll(api chi.Router, modules []Module) {
	for _, m := range modules {
		m.RegisterRoutes(api)
	}
}
