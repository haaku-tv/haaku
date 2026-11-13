package sync

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
)

// newFullSyncApp mounts the routes the way httpapi does: per-type static routes
// first (from perTypeReads), then the contract routes with a :type segment.
func newFullSyncApp(userID string) *fiber.App {
	app := fiber.New()
	v1 := app.Group("/v1")
	v1.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return c.Next()
	})
	sync := v1.Group("/sync")
	registerPerTypeReads(sync)
	RegisterContractRoutes(v1)
	return app
}

// TestCollectionSegmentsEachServeTheirOwnType pins down that the static
// collection routes and the contract :type route cannot bleed into each other:
// a Locals pin or a route match going stale between two requests would show up
// here as one segment answering another segment's rows.
func TestCollectionSegmentsEachServeTheirOwnType(t *testing.T) {
	db := setupPerTypeDB(t)
	seedPerTypeRows(t, db)
	app := newFullSyncApp(perTypeUser)

	cases := []struct {
		path     string
		wantType string
		wantID   float64
	}{
		{"/v1/sync/collection/movies", "movie", 111},
		{"/v1/sync/collection/shows", "show", 211},
		// The :type route, for a segment that has no static registration.
		{"/v1/sync/collection/episodes", "", 0},
		// Repeated in the reverse order: a pool-reuse leak would flip a value.
		{"/v1/sync/collection/shows", "show", 211},
		{"/v1/sync/collection/movies", "movie", 111},
	}

	for _, tc := range cases {
		status, raw := doGET(t, app, tc.path)
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d (%s)", tc.path, status, raw)
		}
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("GET %s: not a contract array: %v (%s)", tc.path, err, raw)
		}
		if tc.wantType == "" {
			// No episode fixture is seeded for the collection, so an empty
			// array is the honest answer — but it must still be an array.
			if items == nil {
				t.Fatalf("GET %s: body is null, want []", tc.path)
			}
			continue
		}
		if len(items) != 1 {
			t.Fatalf("GET %s: %d items, want 1 (%s)", tc.path, len(items), raw)
		}
		item := items[0]
		if item["type"] != tc.wantType {
			t.Errorf("GET %s: type = %v, want %s", tc.path, item["type"], tc.wantType)
		}
		media, ok := item[tc.wantType].(map[string]any)
		if !ok {
			t.Fatalf("GET %s: no %q object: %v", tc.path, tc.wantType, item)
		}
		ids, ok := media["ids"].(map[string]any)
		if !ok {
			t.Fatalf("GET %s: no ids: %v", tc.path, media)
		}
		if got, _ := ids["trakt"].(float64); got != tc.wantID {
			t.Errorf("GET %s: ids.trakt = %v, want %v", tc.path, ids["trakt"], tc.wantID)
		}
	}
}

var _ = database.Movie{}
