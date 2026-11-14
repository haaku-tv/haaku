package discovery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/openapi"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupRecommendationTestDB points the database singleton at an in-memory
// sqlite catalogue holding just the tables the engine reads.
func setupRecommendationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Movie{}, &database.Show{}, &database.Credit{}, &database.Episode{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.SetDBForTest(t, db)
	return db
}

// The recommendation routes are served now. They used to answer an honest 501;
// this pins the replacement so a later change cannot quietly turn them back into
// a stub, and (just as important) cannot turn a missing title into a 500.
func TestRecommendationRoutesAnswerFromTheCatalogue(t *testing.T) {
	db := setupRecommendationTestDB(t)
	now := time.Now().UTC()

	source := database.Movie{
		Title: "Source", TmdbID: 7001, Genres: database.StringSlice{"Comedy", "Short"},
		RatingAvg: 8, RatingCount: 100, Popularity: 50, CreatedAt: now, UpdatedAt: now,
	}
	neighbour := database.Movie{
		Title: "Neighbour", TmdbID: 7002, Genres: database.StringSlice{"Comedy"},
		RatingAvg: 7, RatingCount: 100, Popularity: 40, CreatedAt: now, UpdatedAt: now,
	}
	other := database.Movie{
		Title: "Other", TmdbID: 7003, Genres: database.StringSlice{"Drama"},
		RatingAvg: 9, RatingCount: 100, Popularity: 30, CreatedAt: now, UpdatedAt: now,
	}
	for _, movie := range []*database.Movie{&source, &neighbour, &other} {
		if err := db.Create(movie).Error; err != nil {
			t.Fatalf("seed movie: %v", err)
		}
	}

	app := fiber.New()
	RegisterRoutes(app.Group("/v1"))

	resp, err := app.Test(httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/movies/recommendations/%d", source.ID), nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("no related movies returned for a title with genres")
	}
	if first, _ := body[0]["title"].(string); first != "Neighbour" {
		t.Fatalf("first related title = %q, want the comedy neighbour", first)
	}

	// An id that is not in the catalogue is an honest empty list, not an error.
	respMissing, err := app.Test(httptest.NewRequest(http.MethodGet, "/v1/movies/recommendations/999999", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = respMissing.Body.Close() }()
	if respMissing.StatusCode != http.StatusOK {
		t.Fatalf("unknown id status = %d, want 200 with []", respMissing.StatusCode)
	}
}

// The RouteDef is what puts a status into openapi.yaml, so the spec and the
// handler must agree. These routes used to answer 501 while the recommendations
// engine was missing; they are served now, and a 501 left behind here would
// advertise a working surface as a stub (the parity gate grades on exactly that).
func TestRecommendationRouteDefsServe(t *testing.T) {
	want := map[string]bool{
		"/v1/movies/recommendations/{id}": false,
		"/v1/movies/{id}/related":         false,
		"/v1/shows/recommendations/{id}":  false,
		"/v1/shows/{id}/related":          false,
	}
	for _, d := range openapi.AllRoutes() {
		if _, ok := want[d.Path]; !ok {
			continue
		}
		if _, ok := d.StatusCodes[501]; ok {
			t.Errorf("RouteDef %s still declares a 501 response", d.Path)
		}
		if _, ok := d.StatusCodes[200]; !ok {
			t.Errorf("RouteDef %s declares no 200 response", d.Path)
		}
		want[d.Path] = true
	}
	for path, found := range want {
		if !found {
			t.Errorf("no RouteDef registered for %s", path)
		}
	}
}
