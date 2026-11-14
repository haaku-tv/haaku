package recommendations

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
)

// setupRecDB points the database singleton at an in-memory catalogue.
func setupRecDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Movie{}, &database.Show{}, &database.Credit{}, &database.Episode{},
		&database.UserHistory{}, &database.UserWatchlist{}, &database.UserFavorite{},
		&database.UserRating{}, &database.UserHidden{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.SetDBForTest(t, db)
	return db
}

// recApp builds an app whose handlers see a fixed user id, standing in for the
// auth middleware the real router puts in front of these routes.
func recApp(userID string) *fiber.App {
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("user_id", userID)
		return c.Next()
	})
	RegisterRoutes(app.Group("/v1"))
	return app
}

type recFixture struct {
	comedy1, comedy2, drama1 int64
}

func seedRec(t *testing.T, db *gorm.DB) recFixture {
	t.Helper()
	now := time.Now().UTC()
	mk := func(title string, tmdb int, genres []string, rating float64, popularity int) int64 {
		m := database.Movie{
			Title: title, TmdbID: tmdb, Genres: database.StringSlice(genres),
			RatingAvg: rating, RatingCount: 500, Popularity: float64(popularity),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("seed %s: %v", title, err)
		}
		return m.ID
	}
	return recFixture{
		comedy1: mk("Saved Comedy", 8101, []string{"Comedy"}, 8, 50),
		comedy2: mk("Other Comedy", 8102, []string{"Comedy"}, 7.5, 60),
		drama1:  mk("A Drama", 8103, []string{"Drama"}, 9, 70),
	}
}

func TestPersonalizedMoviesUsesTheWatchlist(t *testing.T) {
	db := setupRecDB(t)
	f := seedRec(t, db)
	now := time.Now().UTC()

	if err := db.Create(&database.UserWatchlist{
		UserID: "local_rec", MediaType: "movie", MediaID: f.comedy1,
		ListedAt: now, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}

	resp, err := recApp("local_rec").Test(
		httptest.NewRequest(http.MethodGet, "/v1/recommendations/movies", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Items        []recMovieItem `json:"items"`
		Personalised bool           `json:"personalised"`
		RowCount     int            `json:"row_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Personalised {
		t.Fatal("a saved title must count as taste")
	}
	if len(body.Items) == 0 {
		t.Fatal("no recommendations for an account with a watchlist")
	}
	if body.Items[0].Title != "Other Comedy" {
		t.Fatalf("first pick = %q, want the comedy neighbour", body.Items[0].Title)
	}
	for _, item := range body.Items {
		if item.ID == f.comedy1 {
			t.Fatal("recommended a title already on the watchlist")
		}
	}
}

func TestPersonalizedMoviesColdStartReturnsPopular(t *testing.T) {
	db := setupRecDB(t)
	seedRec(t, db)

	resp, err := recApp("local_new").Test(
		httptest.NewRequest(http.MethodGet, "/v1/recommendations/movies", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var body struct {
		Items        []recMovieItem `json:"items"`
		Personalised bool           `json:"personalised"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Personalised {
		t.Fatal("an account with no history must report a cold start")
	}
	if len(body.Items) == 0 {
		t.Fatal("cold start returned nothing instead of the popular shelf")
	}
	if body.Items[0].Title != "A Drama" {
		t.Fatalf("cold start led with %q, want the most popular title", body.Items[0].Title)
	}
}

func TestDismissingARecommendationHidesIt(t *testing.T) {
	db := setupRecDB(t)
	f := seedRec(t, db)
	now := time.Now().UTC()

	if err := db.Create(&database.UserWatchlist{
		UserID: "local_hide", MediaType: "movie", MediaID: f.comedy1,
		ListedAt: now, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}

	app := recApp("local_hide")
	resp, err := app.Test(httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/v1/recommendations/movies/%d", f.comedy2), nil))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dismiss status = %d, want 204", resp.StatusCode)
	}

	var hidden database.UserHidden
	if err := db.Where("user_id = ? AND section = ? AND media_id = ?",
		"local_hide", "recommendations", f.comedy2).First(&hidden).Error; err != nil {
		t.Fatalf("no hidden row written: %v", err)
	}

	// Dismissing the same title twice is idempotent, not a 500.
	resp2, err := app.Test(httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/v1/recommendations/movies/%d", f.comedy2), nil))
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("repeat dismiss status = %d, want 204", resp2.StatusCode)
	}
	var count int64
	if err := db.Model(&database.UserHidden{}).
		Where("user_id = ? AND section = ? AND media_id = ?", "local_hide", "recommendations", f.comedy2).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("hidden rows = %d, want 1", count)
	}

	// And the dismissed title no longer comes back.
	listResp, err := app.Test(httptest.NewRequest(http.MethodGet, "/v1/recommendations/movies", nil))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer func() { _ = listResp.Body.Close() }()
	var body struct {
		Items []recMovieItem `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, item := range body.Items {
		if item.ID == f.comedy2 {
			t.Fatal("a dismissed title came back in the next response")
		}
	}
}

func TestDismissingRejectsANonNumericId(t *testing.T) {
	setupRecDB(t)

	resp, err := recApp("local_x").Test(
		httptest.NewRequest(http.MethodDelete, "/v1/recommendations/movies/abc", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
