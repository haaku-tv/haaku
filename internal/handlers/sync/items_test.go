package sync

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// A collection item must carry the media object it points at, not just
// media_type/media_id.
//
// The web pages read `item.movie` / `item.show` — the title, the images and the
// `ids` block a follow-up write echoes back — so an item that names only the
// row renders as nothing, and a remove built from it sends an empty ids object.
// That is what left the watchlist page empty while the row was really stored.

const itemsUser = "local_testuser"

// setupItemsDB mirrors the other sync handler tests: real PostgreSQL when
// DATABASE_URL is set, in-memory sqlite otherwise.
func setupItemsDB(t *testing.T) *gorm.DB {
	t.Helper()
	models := []interface{}{
		&database.Movie{}, &database.Show{}, &database.Episode{}, &database.Season{},
		&database.UserHistory{}, &database.UserCollection{}, &database.UserWatchlist{},
		&database.UserRating{}, &database.UserFavorite{},
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

// seedItems writes one movie, one show, one season and one episode, plus the
// default user's rows for all five collections.
func seedItems(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	movie := database.Movie{TmdbID: 27205, Slug: "inception", Title: "Inception"}
	if err := db.Create(&movie).Error; err != nil {
		t.Fatalf("create movie: %v", err)
	}
	show := database.Show{TmdbID: 1396, Slug: "breaking-bad", Title: "Breaking Bad"}
	if err := db.Create(&show).Error; err != nil {
		t.Fatalf("create show: %v", err)
	}
	season := database.Season{ShowID: show.ID, SeasonNumber: 5, EpisodeCount: 16}
	if err := db.Create(&season).Error; err != nil {
		t.Fatalf("create season: %v", err)
	}
	episode := database.Episode{ShowID: show.ID, SeasonID: season.ID, EpisodeNumber: 14, Title: "Ozymandias"}
	if err := db.Create(&episode).Error; err != nil {
		t.Fatalf("create episode: %v", err)
	}

	rows := []interface{}{
		&database.UserWatchlist{UserID: itemsUser, MediaType: "movie", MediaID: movie.ID, Rank: 1, ListedAt: now},
		&database.UserFavorite{UserID: itemsUser, MediaType: "show", MediaID: show.ID, Rank: 1, FavoritedAt: now},
		&database.UserRating{UserID: itemsUser, MediaType: "movie", MediaID: movie.ID, Rating: 8, RatedAt: now},
		&database.UserCollection{UserID: itemsUser, MediaType: "show", MediaID: show.ID, CollectedAt: now},
		// An episode history row stores the EPISODE id in media_id, so the show
		// has to be resolved through the episode's parent.
		&database.UserHistory{
			UserID: itemsUser, MediaType: "episode", MediaID: episode.ID,
			SeasonID: &season.ID, EpisodeID: &episode.ID, WatchedAt: now,
		},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("create row: %v", err)
		}
	}
}

// getCollection mounts one internal read route and returns its decoded body.
func getCollection(t *testing.T, db *gorm.DB, path string, handler fiber.Handler) (map[string]any, http.Header) {
	t.Helper()
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", itemsUser)
		return c.Next()
	})
	app.Get("/"+path, handler)

	req, err := http.NewRequest(http.MethodGet, "/"+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Host = "example.com"
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("GET /%s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /%s: status %d", path, resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body, resp.Header
}

// itemMovie returns the nested media object of the first item.
func firstItem(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("items = %v, want at least one", body["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item is %T, want an object", items[0])
	}
	return item
}

func TestWatchlistItemCarriesItsMovie(t *testing.T) {
	db := setupItemsDB(t)
	seedItems(t, db)

	body, header := getCollection(t, db, "sync/watchlist", handleGetWatchlist)

	item := firstItem(t, body)
	if item["type"] != "movie" {
		t.Errorf("type = %v, want movie", item["type"])
	}
	if rank, ok := item["rank"].(float64); !ok || rank != 1 {
		t.Errorf("rank = %v, want 1", item["rank"])
	}
	movie, ok := item["movie"].(map[string]any)
	if !ok {
		t.Fatalf("item has no movie object: %v", item)
	}
	if movie["title"] != "Inception" {
		t.Errorf("movie.title = %v, want Inception", movie["title"])
	}
	ids, ok := movie["ids"].(map[string]any)
	if !ok {
		t.Fatalf("movie.ids = %v, want an object", movie["ids"])
	}
	if _, ok := ids["tmdb"]; !ok {
		t.Errorf("movie.ids has no tmdb: %v", ids)
	}
	if got := header.Get("X-Pagination-Total-Items"); got != "1" {
		t.Errorf("X-Pagination-Total-Items = %q, want 1", got)
	}
}

func TestFavoriteRatingCollectionItemsCarryTheirMedia(t *testing.T) {
	db := setupItemsDB(t)
	seedItems(t, db)

	t.Run("favorites", func(t *testing.T) {
		body, _ := getCollection(t, db, "sync/favorites", handleGetFavorites)
		item := firstItem(t, body)
		show, ok := item["show"].(map[string]any)
		if !ok {
			t.Fatalf("favorite item has no show object: %v", item)
		}
		if show["title"] != "Breaking Bad" {
			t.Errorf("show.title = %v, want Breaking Bad", show["title"])
		}
	})

	t.Run("ratings", func(t *testing.T) {
		body, _ := getCollection(t, db, "sync/ratings", handleGetRatings)
		item := firstItem(t, body)
		if item["rating"] != float64(8) {
			t.Errorf("rating = %v, want 8", item["rating"])
		}
		if _, ok := item["movie"].(map[string]any); !ok {
			t.Fatalf("rating item has no movie object: %v", item)
		}
	})

	t.Run("collection", func(t *testing.T) {
		body, _ := getCollection(t, db, "sync/collection", handleGetCollection)
		item := firstItem(t, body)
		if _, ok := item["show"].(map[string]any); !ok {
			t.Fatalf("collection item has no show object: %v", item)
		}
	})
}

func TestHistoryItemCarriesItsEpisodeAndShow(t *testing.T) {
	db := setupItemsDB(t)
	seedItems(t, db)

	body, _ := getCollection(t, db, "sync/history", handleGetHistory)
	item := firstItem(t, body)

	episode, ok := item["episode"].(map[string]any)
	if !ok {
		t.Fatalf("history item has no episode object: %v", item)
	}
	if episode["title"] != "Ozymandias" {
		t.Errorf("episode.title = %v, want Ozymandias", episode["title"])
	}
	if episode["season"] != float64(5) {
		t.Errorf("episode.season = %v, want 5", episode["season"])
	}
	// The parent show comes from the episode row, not from media_id (an
	// episode history row stores the episode id there).
	show, ok := item["show"].(map[string]any)
	if !ok {
		t.Fatalf("history item has no show object: %v", item)
	}
	if show["title"] != "Breaking Bad" {
		t.Errorf("show.title = %v, want Breaking Bad", show["title"])
	}
}

func TestDanglingMediaRowKeepsTheItemWithoutTheObject(t *testing.T) {
	db := setupItemsDB(t)
	seedItems(t, db)
	// A watchlist row whose media row is gone: the item still exists, so the
	// envelope is served and only the media object is omitted.
	if err := db.Create(&database.UserWatchlist{
		UserID: itemsUser, MediaType: "movie", MediaID: 999999, Rank: 2, ListedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("create dangling row: %v", err)
	}

	var rows []database.UserWatchlist
	if err := db.Where("user_id = ? AND media_id = ?", itemsUser, 999999).Find(&rows).Error; err != nil {
		t.Fatalf("read rows: %v", err)
	}
	items, err := buildWatchlistItems(db, rows)
	if err != nil {
		t.Fatalf("build items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if _, ok := items[0]["movie"]; ok {
		t.Errorf("dangling item carries a movie object: %v", items[0])
	}
	if items[0]["media_id"] != int64(999999) {
		t.Errorf("media_id = %v, want 999999", items[0]["media_id"])
	}
}

func TestHydrationFailureIsAnErrorNotASilentPage(t *testing.T) {
	// A page whose media tables are unreachable must not be served as a 200 with
	// the media objects quietly stripped: a client cannot tell that apart from
	// media rows that genuinely no longer exist.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	// The collection table exists; the media tables deliberately do not.
	if err := db.AutoMigrate(&database.UserWatchlist{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Create(&database.UserWatchlist{
		UserID: itemsUser, MediaType: "movie", MediaID: 1, Rank: 1, ListedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("create row: %v", err)
	}

	var rows []database.UserWatchlist
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("read rows: %v", err)
	}
	if _, err := buildWatchlistItems(db, rows); err == nil {
		t.Fatal("buildWatchlistItems returned a page with a failed hydration; want an error")
	}
}
