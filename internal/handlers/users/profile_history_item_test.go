package users

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/gorm"
)

// The public-profile single-item history read is the second surface issue #96
// names: it resolved :media_id with the type-agnostic resolver too, so an
// episode history row (media_type 'episode', media_id = episodes.id) was
// unreachable at /v1/users/{id}/history/episode/{episodes.id}.

// seedProfileEpisodeWatch writes a show/season/episode and one episode watch by
// target.
func seedProfileEpisodeWatch(t *testing.T, db *gorm.DB) {
	t.Helper()
	seed := func(model any) {
		t.Helper()
		if err := db.Create(model).Error; err != nil {
			t.Fatalf("seed %T: %v", model, err)
		}
	}
	seed(&database.Show{ID: 601, TmdbID: 6010, Title: "Breaking Bad", Slug: "breaking-bad"})
	seed(&database.Season{ID: 11, ShowID: 601, SeasonNumber: 1, EpisodeCount: 1})
	seed(&database.Episode{ID: 201, ShowID: 601, SeasonID: 11, EpisodeNumber: 1, Title: "Pilot"})
	seasonID, episodeID := int64(11), int64(201)
	seed(&database.UserHistory{
		UserID: "target", MediaType: "episode", MediaID: episodeID,
		SeasonID: &seasonID, EpisodeID: &episodeID,
		WatchedAt: time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC),
	})
}

// TestProfileHistoryItemAddressesAnEpisode covers the /history/{media_type}/
// {media_id} route (singular type segment). Both the singular and the contract
// plural path must answer the same item.
func TestProfileHistoryItemAddressesAnEpisode(t *testing.T) {
	db := profileTestDB(t)
	seedUser(t, db, "target", false, "active")
	seedProfileEpisodeWatch(t, db)
	app := newProfileReadApp("stranger")

	for _, path := range []string{
		"/v1/users/target/history/episode/201",
		"/v1/users/target/history/episodes/201",
	} {
		status, body := doGET(t, app, path)
		if status != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (%s)", path, status, body)
		}
		if !strings.Contains(body, `"Pilot"`) {
			t.Errorf("GET %s: episode title missing from the item: %s", path, body)
		}
		if !strings.Contains(body, `"Breaking Bad"`) {
			t.Errorf("GET %s: parent show missing from the item: %s", path, body)
		}
	}

	// The wrong-type and missing-id answers stay distinct, as on the sync route.
	status, body := doGET(t, app, "/v1/users/target/history/episodes/9999")
	if status != http.StatusNotFound {
		t.Errorf("GET an id that exists nowhere = %d, want 404 (%s)", status, body)
	}
	if status, _ := doGET(t, app, "/v1/users/target/history/movie/601"); status != http.StatusBadRequest {
		t.Errorf("GET a show id as a movie = %d, want 400", status)
	}

	// A season read is its episodes: user_history stores no season row.
	status, body = doGET(t, app, "/v1/users/target/history/seasons/11")
	if status != http.StatusOK {
		t.Fatalf("GET the episode's season = %d, want 200 (%s)", status, body)
	}
	if !strings.Contains(body, `"Pilot"`) {
		t.Errorf("season read did not serve the season's episode: %s", body)
	}
}
