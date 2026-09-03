package apitest

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// specPath matches a top-level path key in openapi.yaml — a line indented by
// exactly two spaces, starting with "/" and ending in ":". Parsing this much YAML
// by hand is deliberate: the contract check is worth more than the dependency it
// would otherwise need, and the shape it reads is the one line every path entry
// has.
var specPath = regexp.MustCompile(`(?m)^  (/[^\s:]*):\s*$`)

// TestEveryRouteIsInTheContract walks the composed router and the contract beside
// it, and fails on any path present in one and absent from the other.
//
// ⚠ This is the fourth attempt at not shipping a build whose contract disagrees
// with it (home v7, home v8, status v2). The two lists are compared as sets of
// path templates: an operation this service answers but does not document is as
// much a defect as one it documents but does not answer.
func TestEveryRouteIsInTheContract(t *testing.T) {
	a := newAPI(t, 100, 1000)

	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	documented := map[string]bool{}
	for _, m := range specPath.FindAllStringSubmatch(string(raw), -1) {
		documented[m[1]] = true
	}
	if len(documented) < 20 {
		t.Fatalf("found only %d paths in openapi.yaml — the scan is broken, not the contract", len(documented))
	}

	registered := map[string]bool{}
	_ = chi.Walk(a.rt.Mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// OPTIONS routes are the derived preflight handlers, not operations: they
		// exist because chi runs group middleware only on a matched route, and the
		// contract describes them as CORS behaviour rather than as endpoints.
		if method == http.MethodOptions {
			return nil
		}
		registered[strings.TrimSuffix(route, "/")] = true
		return nil
	})

	var undocumented, unimplemented []string
	for route := range registered {
		if !documented[route] {
			undocumented = append(undocumented, route)
		}
	}
	for path := range documented {
		// The health probes are mounted outside /api and are walked too, so they
		// are compared like everything else.
		if !registered[path] {
			unimplemented = append(unimplemented, path)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(unimplemented)

	if len(undocumented) > 0 {
		t.Errorf("routes the service answers but openapi.yaml does not describe: %v", undocumented)
	}
	if len(unimplemented) > 0 {
		t.Errorf("paths openapi.yaml describes but the service does not answer: %v", unimplemented)
	}
}
