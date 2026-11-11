package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// GET /v1/shows/{id}/seasons/{season}/info used to be registered by BOTH the
// seasons package and tmdbmeta (#142). Fiber matches in registration order, so
// one of them was unreachable dead code, and they do not agree: the seasons
// handler can only answer from the season row, where title/overview/TMDB id are
// null, while tmdbmeta serves the provider-backed detail.
//
// These tests go through RegisterRoutes — the real registry — because that is
// the only place the defect was visible. A handler test that mounts its own app
// passes for either implementation, which is how the duplicate survived (#137
// is the same trap).
func newSeasonInfoApp(t *testing.T) (*fiber.App, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:season_info_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.AutoMigrate(database.MediaModels()...); err != nil {
		t.Fatalf("migrate media: %v", err)
	}
	if err := db.AutoMigrate(&database.MediaMetadata{}); err != nil {
		t.Fatalf("migrate media_metadata: %v", err)
	}
	database.SetDBForTest(t, db)

	now := time.Now().UTC()
	// The row fields the contract season object reads from our own store.
	show := database.Show{Title: "Provider Show", TmdbID: 4242, UpdatedAt: now}
	if err := db.Create(&show).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}
	season := database.Season{ShowID: show.ID, SeasonNumber: 1, EpisodeCount: 8, UpdatedAt: now}
	if err := db.Create(&season).Error; err != nil {
		t.Fatalf("seed season: %v", err)
	}

	// A stored provider detail, which is what the provider-backed handler serves
	// without calling TMDB at all (fetchOrStored returns the cached payload).
	// Keys match tmdb.Season's JSON shape.
	if err := database.PutMediaMetadata(db, "season", season.ID, "season_info", database.JSONMap{
		"id":            777,
		"name":          "Provider Season Title",
		"overview":      "Provider overview text",
		"season_number": 1,
		"air_date":      "2020-01-05",
	}); err != nil {
		t.Fatalf("store season_info: %v", err)
	}

	app := fiber.New(fiber.Config{ErrorHandler: response.HandleError})
	RegisterRoutes(app)
	return app, db
}

func EFGH(t *testing.T, app *fiber.App, path string) (int, []byte) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil), -1)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: read body: %v", path, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, raw
}

// TestSeasonInfoServesTheProviderBackedImplementation is the #142 regression.
// It fails if the seasons package registers the path again: that handler answers
// title/overview as null, and this asserts the provider's values.
func TestSeasonInfoServesTheProviderBackedImplementation(t *testing.T) {
	app, db := newSeasonInfoApp(t)

	var show database.Show
	if err := db.Where("tmdb_id = ?", 4242).First(&show).Error; err != nil {
		t.Fatalf("reload show: %v", err)
	}
	status, raw := EFGH(t, app, fmt.Sprintf("/v1/shows/%d/seasons/1/info", show.ID))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", status, raw)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if got["title"] != "Provider Season Title" {
		t.Errorf("title = %v, want the provider's season name — a null here means the row-only seasons handler owns this route again", got["title"])
	}
	if got["overview"] != "Provider overview text" {
		t.Errorf("overview = %v, want the provider's overview", got["overview"])
	}
	ids, _ := got["ids"].(map[string]any)
	if ids == nil || fmt.Sprint(ids["tmdb"]) != "777" {
		t.Errorf("ids.tmdb = %v, want 777 from the provider detail", ids)
	}
	// The row's own fields must still come from the row, not the cached payload.
	if fmt.Sprint(got["aired_episodes"]) != "8" {
		t.Errorf("aired_episodes = %v, want 8 from the season row", got["aired_episodes"])
	}
	if fmt.Sprint(got["number"]) != "1" {
		t.Errorf("number = %v, want 1", got["number"])
	}
}

// TestSeasonInfoUnknownIDsAreNotFound keeps the 404s that the removed seasons
// handler's test covered, now asserted through the live registry.
func TestSeasonInfoUnknownIDsAreNotFound(t *testing.T) {
	app, _ := newSeasonInfoApp(t)

	for _, path := range []string{
		"/v1/shows/999999/seasons/1/info",
		"/v1/shows/1/seasons/999/info",
		"/v1/shows/not-an-int/seasons/1/info",
	} {
		status, raw := EFGH(t, app, path)
		if status != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (%s)", path, status, raw)
		}
	}
}
