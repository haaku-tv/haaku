package updates

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/contract"
	"github.com/haaku-tv/haaku/internal/contractshape"
	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// movieUpdatesTestDB builds a migrated sqlite database holding one movie whose
// updated_at is `updated`, and points the package at it.
func movieUpdatesTestDB(t *testing.T, updated time.Time) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	if sqlDB, serr := db.DB(); serr == nil {
		// One connection keeps the in-memory schema visible to the handler's
		// own queries. Close it in cleanup: the named shared-memory database
		// lives until its last connection closes, so without this a
		// `-count=2` run reuses the first run's rows and the seed collides.
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := db.AutoMigrate(&database.Movie{}, &database.ExternalID{}); err != nil {
		t.Fatalf("migrate test DB: %v", err)
	}
	movie := database.Movie{
		ID: 7, TmdbID: 603, ImdbID: "tt0133093", Slug: "the-matrix", Title: "The Matrix",
		Metadata: database.JSONMap{}, Images: database.JSONMap{},
		CreatedAt: updated, UpdatedAt: updated,
	}
	if err := db.Create(&movie).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	if err := db.Create(&database.ExternalID{MediaType: "movie", MediaID: movie.ID,
		Source: "tmdb", ExternalID: "603"}).Error; err != nil {
		t.Fatalf("seed external id: %v", err)
	}
	// ids.slug comes from the external_ids lookup, not the movies row.
	if err := db.Create(&database.ExternalID{MediaType: "movie", MediaID: movie.ID,
		Source: "slug", ExternalID: "the-matrix"}).Error; err != nil {
		t.Fatalf("seed slug external id: %v", err)
	}
	database.SetDBForTest(t, db)
	return db
}

// movieUpdatesGet serves the feed from a minimal app. The route is public, so
// there is no auth shim: the handler is exercised exactly as the router mounts
// it.
func movieUpdatesGet(t *testing.T, path string) (int, []byte) {
	t.Helper()
	app := fiber.New()
	app.Get("/v1/movies/updates/:start_date", handleMovieUpdates)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil && err.Error() != "EOF" {
		t.Fatalf("read GET %s body: %v", path, err)
	}
	return resp.StatusCode, raw
}

// TestMovieUpdatesFeedServesTheContractItemShape is the regression test for
// GET /movies/updates/{start_date} (#141).
//
// The feed used to answer a bare movie array, so every item was missing the
// item schema's required `updated_at` and `movie` keys. The test asserts the
// item shape field by field and then validates the whole body against the
// vendored contract with the same validator the conformance harness uses, so a
// future field regression fails here rather than in the harness.
func TestMovieUpdatesFeedServesTheContractItemShape(t *testing.T) {
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	movieUpdatesTestDB(t, updated)

	status, raw := movieUpdatesGet(t, "/v1/movies/updates/2026-02-01")
	if status != http.StatusOK {
		t.Fatalf("GET updates feed = %d (%s), want 200", status, raw)
	}

	var body []map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode feed %s: %v", raw, err)
	}
	if len(body) != 1 {
		t.Fatalf("feed rows = %d, want 1 (%s)", len(body), raw)
	}

	// The item's own updated_at is the stored movies.updated_at — the same
	// clock the window filter used — not a sentinel.
	gotUpdated, _ := body[0]["updated_at"].(string)
	if want := contractshape.Timestamp(updated); gotUpdated != want {
		t.Errorf("item updated_at = %q, want the stored timestamp %q", gotUpdated, want)
	}

	movie, ok := body[0]["movie"].(map[string]any)
	if !ok {
		t.Fatalf("item.movie = %T, want the contract's movie object (%s)", body[0]["movie"], raw)
	}
	if movie["title"] != "The Matrix" {
		t.Errorf("item.movie.title = %v, want The Matrix", movie["title"])
	}
	ids, ok := movie["ids"].(map[string]any)
	if !ok {
		t.Fatalf("item.movie.ids = %T, want an object", movie["ids"])
	}
	if ids["trakt"] != float64(7) {
		t.Errorf("item.movie.ids.trakt = %v, want 7 (the stored row id)", ids["trakt"])
	}
	if ids["slug"] != "the-matrix" {
		t.Errorf("item.movie.ids.slug = %v, want the-matrix", ids["slug"])
	}
	// The flat shape is gone: movie fields no longer sit at the item's top level.
	if _, present := body[0]["title"]; present {
		t.Errorf("item has a top-level title — the flat movie array is back (%s)", raw)
	}

	// The whole body, validated against the contract's own item schema.
	doc, err := contract.Load("../../../docs/parity/upstream/trakt-openapi.json")
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	op, ok := doc.Operation("GET", "/movies/updates/{start_date}")
	if !ok {
		t.Fatal("contract has no GET /movies/updates/{start_date}")
	}
	schema, ok := op.ResponseSchema("200")
	if !ok {
		t.Fatal("contract declares no 200 response schema for GET /movies/updates/{start_date}")
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode feed as JSON: %v", err)
	}
	if problems := doc.Validate(schema, decoded); len(problems) != 0 {
		t.Errorf("feed does not satisfy the contract item schema: %v", problems)
	}

	// The same validator must reject the shape this test exists to prevent, or
	// the assertion above proves nothing.
	flat := []any{map[string]any{
		"title": "The Matrix",
		"ids":   map[string]any{"trakt": 7, "slug": "the-matrix"},
	}}
	if problems := doc.Validate(schema, flat); len(problems) == 0 {
		t.Error("the contract schema accepted the old flat movie array — the validation above proves nothing")
	}
}

// TestMovieUpdatesFeedEmptyWindowStaysAnEmptyArray keeps the wrapped feed
// honest at the edges: an empty window is `[]`, not `null`, and it validates.
func TestMovieUpdatesFeedEmptyWindowStaysAnEmptyArray(t *testing.T) {
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	movieUpdatesTestDB(t, updated)

	status, raw := movieUpdatesGet(t, "/v1/movies/updates/2027-01-01")
	if status != http.StatusOK {
		t.Fatalf("GET empty window = %d (%s), want 200", status, raw)
	}
	if string(raw) != "[]" {
		t.Errorf("empty window body = %s, want []", raw)
	}
}
