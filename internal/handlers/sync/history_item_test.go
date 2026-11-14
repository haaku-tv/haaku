package sync

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const historyItemUser = "user-history-item"

// setupHistoryItemDB is the read-side fixture store: real PostgreSQL when
// DATABASE_URL is set, in-memory sqlite otherwise. It holds the tables a
// single-item history read touches — the history rows plus the media rows they
// hydrate from (an episode row's object needs its season and its show).
func setupHistoryItemDB(t *testing.T) *gorm.DB {
	t.Helper()
	models := []any{
		&database.UserHistory{},
		&database.Movie{}, &database.Show{}, &database.Season{}, &database.Episode{},
		&database.Person{},
	}
	var db *gorm.DB
	var err error
	if url := os.Getenv("DATABASE_URL"); url != "" {
		db, err = gorm.Open(postgres.Open(url), &gorm.Config{})
		if err != nil {
			t.Fatalf("open PostgreSQL: %v", err)
		}
		if err := db.Migrator().DropTable(models...); err != nil {
			t.Fatalf("drop stale PG tables: %v", err)
		}
	} else {
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		if err != nil {
			t.Fatalf("open in-memory sqlite: %v", err)
		}
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate test DB: %v", err)
	}
	database.SetDBForTest(t, db)
	return db
}

// newHistoryItemApp mounts the single-item route the way RegisterRoutes does,
// with a stub auth middleware pinning user_id.
func newHistoryItemApp(userID string) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: response.HandleError})
	v1 := app.Group("/v1")
	v1.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return c.Next()
	})
	v1.Get("/sync/history/:media_type/:media_id", handleGetHistoryItem)
	return app
}

// seedHistoryItemRows writes one movie watch and one episode watch for
// historyItemUser, an episode of the same season for a decoy user, and returns
// nothing: the ids are the constants the assertions use.
func seedHistoryItemRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	watched := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	seed := func(model any) {
		t.Helper()
		if err := db.Create(model).Error; err != nil {
			t.Fatalf("seed %T: %v", model, err)
		}
	}

	seed(&database.Movie{ID: 501, TmdbID: 5010, Title: "Heat", Slug: "heat"})
	seed(&database.Show{ID: 601, TmdbID: 6010, Title: "Breaking Bad", Slug: "breaking-bad"})
	seed(&database.Season{ID: 11, ShowID: 601, SeasonNumber: 1, EpisodeCount: 2})
	seed(&database.Episode{ID: 201, ShowID: 601, SeasonID: 11, EpisodeNumber: 1, Title: "Pilot"})
	seed(&database.Episode{ID: 202, ShowID: 601, SeasonID: 11, EpisodeNumber: 2, Title: "Cat's in the Bag"})

	seasonID, episodeID := int64(11), int64(201)
	seed(&database.UserHistory{
		UserID: historyItemUser, MediaType: "movie", MediaID: 501, WatchedAt: watched,
	})
	seed(&database.UserHistory{
		UserID: historyItemUser, MediaType: "episode", MediaID: 201,
		SeasonID: &seasonID, EpisodeID: &episodeID, WatchedAt: watched.Add(time.Hour),
	})
	// Another account's watch of the same episode must not appear in this
	// caller's item read.
	seed(&database.UserHistory{
		UserID: "other-account", MediaType: "episode", MediaID: 201,
		SeasonID: &seasonID, EpisodeID: &episodeID, WatchedAt: watched,
	})
}

// historyItemGET performs one request and returns the status, the decoded body
// and the headers a client pages by.
func historyItemGET(t *testing.T, app *fiber.App, path string) (int, []map[string]any, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", path, err)
	}
	req.Host = "example.com"
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body []map[string]any
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode, body, resp.Header
}

// TestHistoryItemIsHydrated is issue #160: the single-item read answers the
// contract item — the row envelope plus the media object and its ids — not the
// stored row.
func TestHistoryItemIsHydrated(t *testing.T) {
	db := setupHistoryItemDB(t)
	seedHistoryItemRows(t, db)
	app := newHistoryItemApp(historyItemUser)

	status, body, header := historyItemGET(t, app, "/v1/sync/history/movies/501")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/sync/history/movies/501 = %d, want 200", status)
	}
	if len(body) != 1 {
		t.Fatalf("movie item read returned %d rows, want 1", len(body))
	}
	item := body[0]
	if item["type"] != "movie" {
		t.Errorf("type = %v, want movie", item["type"])
	}
	movie, ok := item["movie"].(map[string]any)
	if !ok {
		t.Fatalf("item has no nested movie object: %v", item)
	}
	if movie["title"] != "Heat" {
		t.Errorf("movie.title = %v, want Heat", movie["title"])
	}
	ids, ok := movie["ids"].(map[string]any)
	if !ok {
		t.Fatalf("movie has no ids block: %v", movie)
	}
	if ids["trakt"] != float64(501) {
		t.Errorf("movie.ids.trakt = %v, want 501", ids["trakt"])
	}
	// The contract types this read as a list a client pages, so the pagination
	// convention has to be on it (the conformance row grades exactly this).
	if got := header.Get("X-Pagination-Total-Items"); got != "1" {
		t.Errorf("X-Pagination-Total-Items = %q, want 1", got)
	}
	if got := header.Get("X-Pagination-Page"); got != "1" {
		t.Errorf("X-Pagination-Page = %q, want 1", got)
	}
}

// TestHistoryItemAddressesEpisodesAndSeasons is issue #96: an episode history
// row stores the LOCAL episodes.id, which the type-agnostic resolver never
// looked at, so the route answered 404 (or 400 when a movie shared the number).
func TestHistoryItemAddressesEpisodesAndSeasons(t *testing.T) {
	db := setupHistoryItemDB(t)
	seedHistoryItemRows(t, db)
	app := newHistoryItemApp(historyItemUser)

	for _, path := range []string{
		"/v1/sync/history/episodes/201", // Trakt's plural segment
		"/v1/sync/history/episode/201",  // our singular media_type
		"/v1/sync/history/seasons/11",   // the episode's season
		"/v1/sync/history/shows/601",    // the episode's show
	} {
		status, body, _ := historyItemGET(t, app, path)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, status)
		}
		if len(body) != 1 {
			t.Fatalf("GET %s returned %d rows, want 1", path, len(body))
		}
		episode, ok := body[0]["episode"].(map[string]any)
		if !ok {
			t.Fatalf("GET %s: item has no nested episode object: %v", path, body[0])
		}
		if episode["title"] != "Pilot" {
			t.Errorf("GET %s: episode.title = %v, want Pilot", path, episode["title"])
		}
		// The season number is what tells a client which S/E it is; it lives on
		// the season row, not the episode row.
		if episode["season"] != float64(1) || episode["number"] != float64(1) {
			t.Errorf("GET %s: episode season/number = %v/%v, want 1/1", path, episode["season"], episode["number"])
		}
		show, ok := body[0]["show"].(map[string]any)
		if !ok {
			t.Fatalf("GET %s: item has no parent show: %v", path, body[0])
		}
		if show["title"] != "Breaking Bad" {
			t.Errorf("GET %s: show.title = %v, want Breaking Bad", path, show["title"])
		}
	}
}

// TestHistoryItemKeepsTheRoutesErrorVocabulary locks the three answers the
// route has always given: 404 for an id that exists nowhere, 400 for one that
// belongs to another media type, 400 for an unservable type segment.
func TestHistoryItemKeepsTheRoutesErrorVocabulary(t *testing.T) {
	db := setupHistoryItemDB(t)
	seedHistoryItemRows(t, db)
	app := newHistoryItemApp(historyItemUser)

	cases := []struct {
		path   string
		status int
	}{
		{"/v1/sync/history/episodes/9999", http.StatusNotFound}, // exists nowhere
		{"/v1/sync/history/movies/601", http.StatusBadRequest},  // 601 is a show
		{"/v1/sync/history/shows/501", http.StatusBadRequest},   // 501 is a movie
		{"/v1/sync/history/person/501", http.StatusBadRequest},  // not a history type
	}
	for _, tc := range cases {
		status, _, _ := historyItemGET(t, app, tc.path)
		if status != tc.status {
			t.Errorf("GET %s = %d, want %d", tc.path, status, tc.status)
		}
	}
}
