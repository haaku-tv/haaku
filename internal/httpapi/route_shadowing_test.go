package httpapi

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// segmentMatches reports whether a registered path segment (which may be a
// parameter such as ":id") consumes a concrete path segment.
func segmentMatches(pattern, concrete string) bool {
	return pattern == concrete || strings.HasPrefix(pattern, ":")
}

// pathShadows reports whether the pattern route would capture a request intended
// for the concrete route: same number of segments, every pattern segment either
// literal-equal or a parameter. Both paths are expected to be rooted, e.g.
// "/v1/movies/:id" and "/v1/movies/trending".
func pathShadows(pattern, concrete string) bool {
	if pattern == concrete {
		return false // the same route shadowing "itself" is not shadowing
	}
	p := strings.Split(strings.Trim(pattern, "/"), "/")
	c := strings.Split(strings.Trim(concrete, "/"), "/")
	if len(p) != len(c) {
		return false
	}
	for i := range p {
		if strings.Contains(p[i], "*") {
			return false // wildcards are handled by their own route; keep this precise
		}
		if !segmentMatches(p[i], c[i]) {
			return false
		}
	}
	return true
}

// TestNoStaticRouteIsShadowed is the guard for a real defect class: Fiber matches
// routes in registration order, so a parameterised route registered before a
// static route at the same depth silently swallows it. Six endpoints
// (/v1/movies|shows/{trending,popular} and /v1/lists/{trending,popular}) shipped
// unreachable because media.RegisterRoutes ran before discovery.RegisterRoutes
// and /lists/:id ran before /lists/popular. The handlers were fine; only the
// wiring was wrong, and nothing — not the handler tests, not the OpenAPI
// registry/spec checks — looks at request routing.
//
// The rule this enforces: a static path must never be preceded by an
// earlier-registered parameterised path that would match it. Fix ordering rather
// than adding an exception here.
func TestNoStaticRouteIsShadowed(t *testing.T) {
	app := fiber.New()
	RegisterRoutes(app)

	type route struct {
		method string
		path   string
	}
	var routes []route
	for _, r := range app.GetRoutes() {
		if r.Path == "" || strings.Contains(r.Path, "*") {
			continue
		}
		routes = append(routes, route{method: r.Method, path: r.Path})
	}
	if len(routes) == 0 {
		t.Fatal("no routes registered — the test would pass vacuously")
	}

	for i, later := range routes {
		if strings.Contains(later.path, ":") {
			continue // only static routes can be shadowed
		}
		for _, earlier := range routes[:i] {
			if earlier.method != later.method {
				continue
			}
			if pathShadows(earlier.path, later.path) {
				t.Errorf("%s %s is unreachable: it is shadowed by %s %s, registered earlier",
					later.method, later.path, earlier.method, earlier.path)
			}
		}
	}
}

// TestPathShadows pins the matcher itself, so the guard above cannot pass by
// being unable to detect anything.
func TestPathShadows(t *testing.T) {
	cases := []struct {
		pattern, concrete string
		want              bool
	}{
		{"/v1/movies/:id", "/v1/movies/trending", true},
		{"/v1/movies/trending", "/v1/movies/trending", false},
		{"/v1/movies/:id", "/v1/movies/:id", false},
		{"/v1/movies/updates/:d", "/v1/movies/updates/2026-01-01", true},
		{"/v1/movies/:id", "/v1/movies/updates/2026-01-01", false}, // different depth
		{"/v1/movies/trending", "/v1/movies/popular", false},
	}
	for _, tc := range cases {
		if got := pathShadows(tc.pattern, tc.concrete); got != tc.want {
			t.Errorf("pathShadows(%q, %q) = %v, want %v", tc.pattern, tc.concrete, got, tc.want)
		}
	}
}

// TestNoRouteIsRegisteredTwice is the guard for a second defect class that
// TestNoStaticRouteIsShadowed cannot see: two packages registering the SAME
// method+path.
//
// Fiber matches in registration order, so the first registration wins and the
// second is dead code — and pathShadows explicitly returns false for a pattern
// identical to the concrete path, because "a route shadowing itself" is not
// shadowing. Nothing else looks at this: the OpenAPI registry happily accepts
// both RouteDefs, so `make api-check` is green while one handler is unreachable.
//
// /shows/{id}/seasons/{season}/info was registered by BOTH seasons and tmdbmeta
// (#142). They are not equivalent: the seasons copy can only answer from the
// season row (null title/overview/TMDB id) while tmdbmeta serves the
// provider-backed season detail, so which payload a client got depended on the
// order of two lines in routes.go. Same shape as #137, where /watched/movies
// shadowed /watched/:type with a different implementation.
//
// Fix by giving the path a single owner. Never delete this test to make room for
// a second registration.
func TestNoRouteIsRegisteredTwice(t *testing.T) {
	app := fiber.New()
	RegisterRoutes(app)

	seen := map[string]bool{}
	got := map[string]bool{}
	total := 0
	for _, r := range app.GetRoutes() {
		if r.Path == "" || strings.Contains(r.Path, "*") {
			continue
		}
		// Fiber mirrors every GET as a HEAD. Counting both doubles every finding
		// without adding information, so the guard reads GET only.
		if r.Method == fiber.MethodHead {
			continue
		}
		total++
		key := r.Method + " " + r.Path
		if seen[key] {
			got[key] = true
			continue
		}
		seen[key] = true
	}
	if total == 0 {
		t.Fatal("no routes registered — the test would pass vacuously")
	}

	// knownDuplicates is debt found when this guard was added, and it may only
	// SHRINK (the same ratchet rule as tools/size-baseline.txt). Every entry is a
	// path two packages register: media/contract_metadata.go and tmdbmeta both
	// implement the #68 metadata surface, and media registers first, so tmdbmeta's
	// copy is unreachable. Choosing which implementation owns each route changes
	// the response (media serves the hydrated row and answers 502; tmdbmeta serves
	// the media_metadata cache and answers 503), so it is a deliberate
	// consolidation with its own conformance evidence, not a line to delete here —
	// tracked as its own issue. The guard's job is that the list never grows.
	//
	// When you consolidate one, delete its line: this test fails on a stale entry
	// as loudly as on a new duplicate.
	knownDuplicates := map[string]bool{
		"GET /v1/movies/:id/releases/:country":                             true,
		"GET /v1/movies/:id/studios":                                       true,
		"GET /v1/movies/:id/translations":                                  true,
		"GET /v1/movies/:id/videos":                                        true,
		"GET /v1/shows/:id/certifications":                                 true,
		"GET /v1/shows/:id/seasons/:season/episodes/:episode/translations": true,
		"GET /v1/shows/:id/seasons/:season/episodes/:episode/videos":       true,
		"GET /v1/shows/:id/seasons/:season/episodes/:episode/watching":     true,
		"GET /v1/shows/:id/seasons/:season/videos":                         true,
		"GET /v1/shows/:id/seasons/:season/watching":                       true,
		"GET /v1/shows/:id/studios":                                        true,
		"GET /v1/shows/:id/videos":                                         true,
	}

	for dup := range got {
		if !knownDuplicates[dup] {
			t.Errorf("%s is registered more than once and is not in knownDuplicates: exactly one implementation may own a method+path, because the later registration is unreachable dead code", dup)
		}
	}
	for dup := range knownDuplicates {
		if !got[dup] {
			t.Errorf("%s is listed in knownDuplicates but is no longer registered twice — delete its line so the debt list only shrinks", dup)
		}
	}
	if len(got) > len(knownDuplicates) {
		t.Errorf("duplicate routes grew to %d, want at most %d", len(got), len(knownDuplicates))
	}
}
