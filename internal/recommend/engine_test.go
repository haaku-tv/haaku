package recommend

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
)

// openTestDB opens an in-memory sqlite database with the tables the engine
// reads. The engine is written against both dialects, so the sqlite run is the
// one that proves the serialised-JSON genre matching works.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	models := []any{
		&database.Movie{}, &database.Show{}, &database.Credit{}, &database.Episode{},
		&database.UserHistory{}, &database.UserWatchlist{},
		&database.UserFavorite{}, &database.UserRating{}, &database.UserHidden{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type fixture struct {
	comedy1, comedy2, drama1, thriller1, unseenBest int64 // movies
	show1, show2                                    int64
	director, actor                                 int64
}

// seed lays out a catalogue where the "right" answers are known:
//
//	comedy1  Comedy, Short    rated 8.0 by 400 people   lead: actor A
//	comedy2  Comedy           rated 7.0 by 300 people   lead: actor A   <- the expected neighbour of comedy1
//	drama1   Drama            rated 9.0 by 900 people   lead: actor B
//	thriller1 Thriller        rated 9.5 by 1000 people  lead: nobody
//	unseenBest Comedy, Drama  rated 9.9 by 2000 people  (the catalogue heavyweight)
func seed(t *testing.T, db *gorm.DB) fixture {
	t.Helper()
	now := time.Now().UTC()
	var f fixture

	mkMovie := func(title string, tmdb int, genres []string, rating float64, votes int64, popularity int) int64 {
		m := database.Movie{
			Title:       title,
			TmdbID:      tmdb,
			Genres:      database.StringSlice(genres),
			RatingAvg:   rating,
			RatingCount: votes,
			Popularity:  float64(popularity),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("seed movie %s: %v", title, err)
		}
		return m.ID
	}

	f.comedy1 = mkMovie("Office Party", 101, []string{"Comedy", "Short"}, 8.0, 400, 90)
	f.comedy2 = mkMovie("Second Act", 102, []string{"Comedy"}, 7.0, 300, 70)
	f.drama1 = mkMovie("Quiet Year", 103, []string{"Drama"}, 9.0, 900, 80)
	f.thriller1 = mkMovie("Night Ferry", 104, []string{"Thriller"}, 9.5, 1000, 60)
	f.unseenBest = mkMovie("Everything Everywhere", 105, []string{"Comedy", "Drama"}, 9.9, 2000, 100)

	show := database.Show{
		Title: "Half Hour", TmdbID: 201, Genres: database.StringSlice{"Comedy"},
		RatingAvg: 8.5, RatingCount: 500, Popularity: 75,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&show).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}
	f.show1 = show.ID

	show2 := database.Show{
		Title: "Long Night", TmdbID: 202, Genres: database.StringSlice{"Drama"},
		RatingAvg: 8.0, RatingCount: 200, Popularity: 40,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&show2).Error; err != nil {
		t.Fatalf("seed show 2: %v", err)
	}
	f.show2 = show2.ID

	// comedy1 and comedy2 share a director and a lead actor; drama1 and
	// thriller1 share nobody with comedy1.
	f.director = 900
	f.actor = 901
	other := int64(902)
	for _, credit := range []database.Credit{
		{PersonID: f.director, MediaType: "movie", MediaID: f.comedy1, CreditType: "crew", Job: "Director"},
		{PersonID: f.actor, MediaType: "movie", MediaID: f.comedy1, CreditType: "cast", Order: 0},
		{PersonID: f.director, MediaType: "movie", MediaID: f.comedy2, CreditType: "crew", Job: "Director"},
		{PersonID: f.actor, MediaType: "movie", MediaID: f.comedy2, CreditType: "cast", Order: 0},
		{PersonID: other, MediaType: "movie", MediaID: f.drama1, CreditType: "cast", Order: 0},
	} {
		if err := db.Create(&credit).Error; err != nil {
			t.Fatalf("seed credit: %v", err)
		}
	}

	return f
}

func TestRelatedPrefersSharedGenresAndPeople(t *testing.T) {
	db := openTestDB(t)
	f := seed(t, db)

	items, err := Related(db, "movie", f.comedy1, 3)
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("Related returned nothing for a title with genres")
	}
	if items[0].ID != f.comedy2 {
		// comedy2 shares both a genre and the director+lead actor with comedy1;
		// no other row does.
		t.Fatalf("first neighbour = %d, want comedy2 (%d); all = %+v", items[0].ID, f.comedy2, items)
	}
	for _, item := range items {
		if item.ID == f.comedy1 {
			t.Fatal("Related returned the seed title itself")
		}
		if item.Kind != "" && item.Kind != "movie" {
			t.Fatalf("item kind = %q, want movie", item.Kind)
		}
	}
}

func TestRelatedIsEmptyForATitleWithoutGenresOrPeople(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	lonely := database.Movie{Title: "Silent", TmdbID: 300, Genres: database.StringSlice{}, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&lonely).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	items, err := Related(db, "movie", lonely.ID, 5)
	if err != nil {
		t.Fatalf("Related: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Related = %+v, want no neighbours for a bare title", items)
	}
}

func TestPersonalUsesWatchlistEvenWithoutHistory(t *testing.T) {
	db := openTestDB(t)
	f := seed(t, db)

	// The account has saved a comedy and nothing else: no history, no rating.
	watch := database.UserWatchlist{
		UserID: "local_1", MediaType: "movie", MediaID: f.comedy1,
		ListedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&watch).Error; err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}

	items, personalised, err := Personal(db, "local_1", "movie", 3)
	if err != nil {
		t.Fatalf("Personal: %v", err)
	}
	if !personalised {
		t.Fatal("a watchlist entry must count as taste, not as a cold start")
	}
	if len(items) == 0 {
		t.Fatal("Personal returned nothing")
	}
	if items[0].ID != f.comedy2 {
		t.Fatalf("top pick = %d, want the comedy neighbour (%d); all = %+v", items[0].ID, f.comedy2, items)
	}
	for _, item := range items {
		if item.ID == f.comedy1 {
			t.Fatal("Personal recommended a title already on the watchlist")
		}
	}
}

func TestPersonalFallsBackToPopularForANewAccount(t *testing.T) {
	db := openTestDB(t)
	f := seed(t, db)

	items, personalised, err := Personal(db, "local_new", "movie", 3)
	if err != nil {
		t.Fatalf("Personal: %v", err)
	}
	if personalised {
		t.Fatal("an account with no opinion must report a cold start")
	}
	if len(items) != 3 {
		t.Fatalf("cold start returned %d items, want 3", len(items))
	}
	if items[0].ID != f.unseenBest {
		t.Fatalf("cold start top = %d, want the most popular row (%d)", items[0].ID, f.unseenBest)
	}
}

func TestPersonalSkipsHiddenTitles(t *testing.T) {
	db := openTestDB(t)
	f := seed(t, db)
	now := time.Now().UTC()

	if err := db.Create(&database.UserWatchlist{
		UserID: "local_2", MediaType: "movie", MediaID: f.comedy1,
		ListedAt: now, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}
	if err := db.Create(&database.UserHidden{
		UserID: "local_2", Section: HiddenSection, MediaType: "movie",
		MediaID: f.comedy2, HiddenAt: now,
	}).Error; err != nil {
		t.Fatalf("seed hidden: %v", err)
	}

	items, _, err := Personal(db, "local_2", "movie", 5)
	if err != nil {
		t.Fatalf("Personal: %v", err)
	}
	for _, item := range items {
		if item.ID == f.comedy2 {
			t.Fatal("a dismissed title came back in the recommendations")
		}
	}
}

func TestPersonalKeepsKindsApart(t *testing.T) {
	db := openTestDB(t)
	f := seed(t, db)
	now := time.Now().UTC()

	// A movie taste must not recommend shows and vice versa.
	if err := db.Create(&database.UserWatchlist{
		UserID: "local_3", MediaType: "movie", MediaID: f.comedy1,
		ListedAt: now, CreatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	items, _, err := Personal(db, "local_3", "show", 5)
	if err != nil {
		t.Fatalf("Personal shows: %v", err)
	}
	// The fixture holds two shows and five movies; a show request that came back
	// with more than two rows has been reading the movies table.
	if len(items) > 2 {
		t.Fatalf("show recommendations returned %d rows for a catalogue of 2 shows", len(items))
	}
	for _, item := range items {
		if item.ID != f.show1 && item.ID != f.show2 {
			t.Fatalf("show recommendations returned id %d, not a show", item.ID)
		}
	}
}

func TestTasteProfileWeightsRatingsAroundTheMidpoint(t *testing.T) {
	db := openTestDB(t)
	f := seed(t, db)
	now := time.Now().UTC()

	// 10/10 on a comedy and 1/10 on a thriller: Comedy must surface, Thriller
	// must not.
	for _, r := range []database.UserRating{
		{UserID: "local_4", MediaType: "movie", MediaID: f.comedy1, Rating: 10, RatedAt: now},
		{UserID: "local_4", MediaType: "movie", MediaID: f.thriller1, Rating: 1, RatedAt: now},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatalf("seed rating: %v", err)
		}
	}

	profile, err := tasteProfile(db, "local_4", "movie")
	if err != nil {
		t.Fatalf("tasteProfile: %v", err)
	}
	if profile.Genres["Comedy"] <= 0 {
		t.Fatalf("Comedy weight = %v, want positive", profile.Genres["Comedy"])
	}
	if _, ok := profile.Genres["Thriller"]; ok {
		t.Fatalf("Thriller survived as a preference at %v", profile.Genres["Thriller"])
	}

	items, _, err := Personal(db, "local_4", "movie", 5)
	if err != nil {
		t.Fatalf("Personal: %v", err)
	}
	if len(items) == 0 || items[0].ID == f.thriller1 {
		t.Fatalf("recommendations led with the disliked title: %+v", items)
	}
}

func TestQualityDiscountsThinVoteCounts(t *testing.T) {
	popular := quality(9.0, 5000)
	thin := quality(9.0, 2)
	if !(popular > thin) {
		t.Fatalf("a 9.0 from 5000 votes (%v) must outrank a 9.0 from 2 (%v)", popular, thin)
	}
	if unknown := quality(0, 0); unknown <= 0 || unknown >= popular {
		t.Fatalf("unrated quality = %v, want a middling value", unknown)
	}
}
