package stats

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	statsengine "github.com/haaku-tv/haaku/internal/stats"

	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Contract payload (issue #98)
//
// The route used to answer flat counts. These tests assert the contract's
// nested object end to end: they seed real rows in the tables the payload is
// aggregated from, call the wired handler, and decode the JSON body.
// ---------------------------------------------------------------------------

// seedStatsMarker writes a user_stats row computed "now" so the handler takes
// the fresh path. The row is only the compute marker now (the numbers are
// aggregated from the account tables), and the fresh path is the one sqlite can
// run: the stale path takes a pg advisory lock.
func seedStatsMarker(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	now := time.Now()
	if err := db.Create(&database.UserStats{UserID: userID, ComputedAt: &now}).Error; err != nil {
		t.Fatalf("seed stats marker %s: %v", userID, err)
	}
}

func seedMovie(t *testing.T, db *gorm.DB, runtime int) database.Movie {
	t.Helper()
	m := database.Movie{Title: "movie", TmdbID: runtime, Runtime: runtime}
	if err := db.Create(&m).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	return m
}

func seedEpisode(t *testing.T, db *gorm.DB, showID, seasonID int64, episodeNumber, runtime int) database.Episode {
	t.Helper()
	e := database.Episode{
		ShowID: showID, SeasonID: seasonID, EpisodeNumber: episodeNumber,
		Title: "episode", Runtime: runtime,
	}
	if err := db.Create(&e).Error; err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	return e
}

// seedHistory writes one play. watchedAt and sourceID are parameters rather
// than defaults because the migrations dedupe on
// (user, media_type, media_id, source, source_id) — watched_at is NOT part of
// the key — so a second play of the same title is only storable when it carries
// its own upstream event id. A fixture that inserts both with source="api" and
// no source_id is storable on sqlite (where the expression index does not
// exist) and rejected by PostgreSQL with a duplicate-key violation, which is
// exactly what a fixture that only runs on the lax engine hides.
func seedHistory(t *testing.T, db *gorm.DB, userID, mediaType string, mediaID int64, watchedAt time.Time, sourceID string, seasonID, episodeID *int64) {
	t.Helper()
	h := database.UserHistory{
		UserID: userID, MediaType: mediaType, MediaID: mediaID,
		SeasonID: seasonID, EpisodeID: episodeID,
		WatchedAt: watchedAt, Source: "api",
	}
	if sourceID != "" {
		h.SourceID = &sourceID
	}
	if err := db.Create(&h).Error; err != nil {
		t.Fatalf("seed history %s %d: %v", mediaType, mediaID, err)
	}
}

func seedCollected(t *testing.T, db *gorm.DB, userID, mediaType string, mediaID int64) {
	t.Helper()
	if err := db.Create(&database.UserCollection{
		UserID: userID, MediaType: mediaType, MediaID: mediaID, CollectedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed collection %s %d: %v", mediaType, mediaID, err)
	}
}

func seedRating(t *testing.T, db *gorm.DB, userID, mediaType string, mediaID int64, rating int) {
	t.Helper()
	if err := db.Create(&database.UserRating{
		UserID: userID, MediaType: mediaType, MediaID: mediaID,
		Rating: rating, RatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed rating %s %d: %v", mediaType, mediaID, err)
	}
}

func seedComment(t *testing.T, db *gorm.DB, userID, mediaType string) {
	t.Helper()
	if err := db.Create(&database.Comment{
		UserID: userID, MediaType: mediaType, CommentType: "shout", Body: "hi",
	}).Error; err != nil {
		t.Fatalf("seed comment %q: %v", mediaType, err)
	}
}

func seedFollow(t *testing.T, db *gorm.DB, follower, following string) {
	t.Helper()
	if err := db.Create(&database.UserFollow{
		FollowerID: follower, FollowingID: following, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed follow %s->%s: %v", follower, following, err)
	}
}

// seedActivity lays down a known amount of activity for one user:
//
//	movies      2 titles, 3 play rows (one rewatch), 320 minutes
//	episodes    3 episodes over 2 shows, 4 play rows, 165 minutes
//	collection  1 movie + 2 shows + 1 episode
//	ratings     movie 8, episode 7, show 9, season 5 (4 ratings)
//	comments    2 on movies, 1 on a show, 1 on a list (must not be counted)
//	network     2 followers, 2 following, 1 of them mutual
func seedActivity(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()

	m1 := seedMovie(t, db, 100)
	m2 := seedMovie(t, db, 120)
	// Two plays of m1: distinct play times AND distinct source ids, because the
	// dedupe key is (user, media, source, source_id) — see seedHistory.
	at := time.Now().Add(-24 * time.Hour)
	seedHistory(t, db, userID, "movie", m1.ID, at, "play-1", nil, nil)
	seedHistory(t, db, userID, "movie", m1.ID, at.Add(time.Hour), "play-2", nil, nil)
	seedHistory(t, db, userID, "movie", m2.ID, at.Add(2*time.Hour), "play-3", nil, nil)

	// The episode rows below point at shows 1/2 and seasons 11/21, so those
	// parents have to exist: sqlite's AutoMigrate schema does not enforce the
	// references, a database built from the SQL migrations does, and an insert
	// that only works on the lax engine is a fixture that lies.
	for _, show := range []database.Show{{ID: 1, Title: "show"}, {ID: 2, Title: "show"}} {
		if err := db.Create(&show).Error; err != nil {
			t.Fatalf("seed show %d: %v", show.ID, err)
		}
	}
	for _, season := range []database.Season{{ID: 11, ShowID: 1, SeasonNumber: 1}, {ID: 21, ShowID: 2, SeasonNumber: 1}} {
		if err := db.Create(&season).Error; err != nil {
			t.Fatalf("seed season %d: %v", season.ID, err)
		}
	}

	e1 := seedEpisode(t, db, 1, 11, 1, 45)
	e2 := seedEpisode(t, db, 1, 11, 2, 30)
	e3 := seedEpisode(t, db, 2, 21, 1, 60)
	// e2 is played twice, so each row carries its own source id (same dedupe key).
	for i, e := range []database.Episode{e1, e2, e2, e3} {
		seasonID, episodeID := e.SeasonID, e.ID
		seedHistory(t, db, userID, "episode", e.ID, at.Add(time.Duration(3+i)*time.Hour),
			fmt.Sprintf("ep-play-%d", i+1), &seasonID, &episodeID)
	}

	seedCollected(t, db, userID, "movie", m1.ID)
	seedCollected(t, db, userID, "show", 1)
	seedCollected(t, db, userID, "show", 2)
	seedCollected(t, db, userID, "episode", e1.ID)

	seedRating(t, db, userID, "movie", m1.ID, 8)
	seedRating(t, db, userID, "episode", e1.ID, 7)
	seedRating(t, db, userID, "show", 1, 9)
	seedRating(t, db, userID, "season", 11, 5)

	seedComment(t, db, userID, "movie")
	seedComment(t, db, userID, "movie")
	seedComment(t, db, userID, "show")
	seedComment(t, db, userID, "") // a list comment: no media type

	// The follower users have to exist before the follow rows: PostgreSQL
	// enforces the foreign key the sqlite AutoMigrate schema does not.
	for _, follower := range []string{"fan-a", "fan-b", "fan-c"} {
		seedStatsUser(t, db, follower, false, false)
	}
	seedFollow(t, db, "fan-a", userID)
	seedFollow(t, db, "fan-b", userID)
	seedFollow(t, db, userID, "fan-b")
	seedFollow(t, db, userID, "fan-c")
}

func decodeStatsPayload(t *testing.T, body string) statsengine.Payload {
	t.Helper()
	var payload statsengine.Payload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode stats payload: %v (%s)", err, body)
	}
	return payload
}

func TestUserStatsContractPayload(t *testing.T) {
	db := statsPrivacyDB(t)
	seedStatsUser(t, db, "target", false, false)
	seedStatsMarker(t, db, "target")
	seedActivity(t, db, "target")

	status, body := statsDoGET(t, newStatsApp(""), "/v1/users/target/stats")
	if status != http.StatusOK {
		t.Fatalf("stats: want 200, got %d (%s)", status, body)
	}

	want := statsengine.Payload{
		Movies: statsengine.MediaStats{
			Plays: 3, Minutes: 320, Watched: 2, Collected: 1, Ratings: 1, Comments: 2,
		},
		Episodes: statsengine.MediaStats{
			Plays: 4, Minutes: 165, Watched: 3, Collected: 1, Ratings: 1, Comments: 0,
		},
		Shows: statsengine.ShowStats{
			Watched: 2, Collected: 2, Ratings: 1, Comments: 1,
		},
		Seasons: statsengine.SeasonStats{Ratings: 1, Comments: 0},
		Network: statsengine.NetworkStats{Friends: 1, Followers: 2, Following: 2},
		Ratings: statsengine.RatingStats{
			Total: 4,
			Distribution: map[string]float64{
				"1": 0, "2": 0, "3": 0, "4": 0,
				"5": 25, "6": 0, "7": 25, "8": 25, "9": 25, "10": 0,
			},
		},
		TotalPlays:   7,
		TotalMinutes: 485,
	}

	got := decodeStatsPayload(t, body)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stats payload mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestUserStatsEmptyUserAnswersZeroBlocks(t *testing.T) {
	db := statsPrivacyDB(t)
	seedStatsUser(t, db, "target", false, false)
	seedStatsMarker(t, db, "target")

	status, body := statsDoGET(t, newStatsApp(""), "/v1/users/target/stats")
	if status != http.StatusOK {
		t.Fatalf("stats: want 200, got %d (%s)", status, body)
	}

	// The blocks must be present, not omitted: a client reading
	// stats.movies.plays has to find a number, not a missing path.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode body: %v (%s)", err, body)
	}
	for _, key := range []string{
		"movies", "shows", "seasons", "episodes", "network", "ratings",
		"total_plays", "total_minutes",
	} {
		if _, ok := raw[key]; !ok {
			t.Errorf("payload is missing the %q block: %s", key, body)
		}
	}

	got := decodeStatsPayload(t, body)
	want := statsengine.EmptyPayload()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty user payload mismatch\n got: %+v\nwant: %+v", got, want)
	}
	if len(got.Ratings.Distribution) != 10 {
		t.Errorf("distribution has %d buckets, want 10: %v", len(got.Ratings.Distribution), got.Ratings.Distribution)
	}
	for bucket, share := range got.Ratings.Distribution {
		if share != 0 {
			t.Errorf("distribution[%s] = %v, want 0", bucket, share)
		}
	}
}
