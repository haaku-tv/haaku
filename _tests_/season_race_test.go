package tests

import (
	"testing"

	"github.com/haaku-tv/haaku/external/tmdb"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/metadata"

	"gorm.io/gorm"
)

// TestIngestSeason_RecoversFromAConcurrentInsert pins the fix for the 500 two
// concurrent resolves of one title produced on /v1/media/resolve: both lookups
// miss, both INSERT, the unique index rejects the loser, and the request failed
// even though the season row existed.
//
// The interleaving is forced deterministically instead of raced: a GORM query
// callback inserts the competing row immediately after IngestSeason's lookup
// comes back "not found", which is exactly the window a second request lands in.
func TestIngestSeason_RecoversFromAConcurrentInsert(t *testing.T) {
	// No implicit per-statement transaction: the injected competitor write has
	// to survive our failed INSERT, which is what happens when it comes from a
	// DIFFERENT connection (another request), and cannot when both are in one.
	db := setupTestDB(t).Session(&gorm.Session{SkipDefaultTransaction: true})

	// The constraint the SQL migration creates and the model does not declare.
	// Without it there is no conflict to lose.
	if err := db.Exec(`CREATE UNIQUE INDEX seasons_show_id_season_number ON seasons(show_id, season_number)`).Error; err != nil {
		t.Fatalf("create unique index: %v", err)
	}

	show := &tmdb.Show{
		ID:            4242,
		Name:          "Concurrent Season Show",
		Status:        "Returning Series",
		SeasonsCount:  1,
		EpisodesCount: 1,
		Genres:        []tmdb.Genre{},
		Language:      "en",
	}
	dbShow, err := metadata.IngestShow(db, show, nil, nil)
	if err != nil {
		t.Fatalf("IngestShow failed: %v", err)
	}

	injected := false
	const callback = "test:inject_competitor_season"
	if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if injected || tx.Statement.Table != "seasons" {
			return
		}
		injected = true
		tx.Session(&gorm.Session{NewDB: true}).Exec(
			`INSERT INTO seasons (show_id, tmdb_show_id, season_number, episode_count, created_at, updated_at, images)`+
				` VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, '{}')`,
			dbShow.ID, 4242, 1, 9)
	}); err != nil {
		t.Fatalf("register callback: %v", err)
	}
	defer func() { _ = db.Callback().Create().Remove(callback) }()

	season, err := metadata.IngestSeason(db, dbShow.ID, 4242, tmdb.SeasonSummary{SeasonNumber: 1, EpisodeCount: 5})
	if err != nil {
		t.Fatalf("IngestSeason reported the lost insert as an error: %v", err)
	}
	if !injected {
		t.Fatal("the competitor row was never injected; the test did not exercise the race")
	}
	if season == nil || season.ID == 0 {
		t.Fatalf("IngestSeason returned no season id: %+v", season)
	}

	var stored database.Season
	if err := db.Where("show_id = ? AND season_number = ?", dbShow.ID, 1).First(&stored).Error; err != nil {
		t.Fatalf("read the stored season: %v", err)
	}
	if stored.ID != season.ID {
		t.Errorf("returned season id %d is not the row that exists (%d)", season.ID, stored.ID)
	}

	var count int64
	db.Model(&database.Season{}).Where("show_id = ? AND season_number = ?", dbShow.ID, 1).Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 season row, got %d", count)
	}
}
