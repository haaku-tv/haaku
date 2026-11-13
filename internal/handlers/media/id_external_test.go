package media

import (
	"net/http"
	"testing"

	"github.com/haaku-tv/haaku/internal/database"
)

// An external id can live in two places: the external_ids lookup table, and the
// media row's own column. Reading only the table answered 404 for a title the
// catalogue held by column — which is every row the TMDB backfill wrote and every
// row ingested before the lookup table existed — and that is what made a
// canonical `?id_type=tmdb` URL unusable. These tests pin both sources.
//
// They matter beyond the API: the web app addresses /shows/{tmdb_id} and
// /movies/{tmdb_id} so a URL survives a database reset, and every one of those
// page loads goes through ?id_type=tmdb.

func TestExternalIDResolvesFromTheRowColumn(t *testing.T) {
	db := setupShowsDB(t)

	// No external_ids rows are created here on purpose: the id is on the row.
	if err := db.Create(&database.Show{
		ID: 7, Title: "Severance", TmdbID: 95396, TvdbID: 371980, ImdbID: "tt11280740", Slug: "severance",
	}).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}
	if err := db.Create(&database.Movie{
		ID: 9, Title: "The Matrix", TmdbID: 603, TvdbID: 169, ImdbID: "tt0133093", Slug: "the-matrix",
	}).Error; err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	app := scopeApp()

	t.Run("tmdb id on the show column", func(t *testing.T) {
		resp := scopeGet(t, app, "/v1/shows/95396?id_type=tmdb")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/shows/95396?id_type=tmdb = %d (%s), want 200", resp.StatusCode, errorMessage(t, resp))
		}
		if got := titleOf(t, resp); got != "Severance" {
			t.Errorf("resolved to %q, want Severance", got)
		}
	})

	t.Run("tvdb id on the movie column", func(t *testing.T) {
		resp := scopeGet(t, app, "/v1/movies/169?id_type=tvdb")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/movies/169?id_type=tvdb = %d (%s), want 200", resp.StatusCode, errorMessage(t, resp))
		}
		if got := titleOf(t, resp); got != "The Matrix" {
			t.Errorf("resolved to %q, want The Matrix", got)
		}
	})

	t.Run("imdb id on the row", func(t *testing.T) {
		resp := scopeGet(t, app, "/v1/shows/tt11280740?id_type=imdb")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/shows/tt11280740?id_type=imdb = %d (%s), want 200", resp.StatusCode, errorMessage(t, resp))
		}
		if got := titleOf(t, resp); got != "Severance" {
			t.Errorf("resolved to %q, want Severance", got)
		}
	})

	t.Run("slug on the row", func(t *testing.T) {
		resp := scopeGet(t, app, "/v1/movies/the-matrix")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/movies/the-matrix = %d (%s), want 200", resp.StatusCode, errorMessage(t, resp))
		}
		if got := titleOf(t, resp); got != "The Matrix" {
			t.Errorf("resolved to %q, want The Matrix", got)
		}
	})

	t.Run("the route's type still decides a numeric external id", func(t *testing.T) {
		// 603 is the movie's TMDB id; asking the show route for it must not
		// serve the movie, and must not claim the id is unknown either.
		resp := scopeGet(t, app, "/v1/shows/603?id_type=tmdb")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /v1/shows/603?id_type=tmdb = %d, want 404", resp.StatusCode)
		}
		if msg := errorMessage(t, resp); msg != "not a show" {
			t.Errorf("message = %q, want %q", msg, "not a show")
		}
	})

	t.Run("an id nobody carries is media not found", func(t *testing.T) {
		resp := scopeGet(t, app, "/v1/shows/555555?id_type=tmdb")
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /v1/shows/555555?id_type=tmdb = %d, want 404", resp.StatusCode)
		}
		if msg := errorMessage(t, resp); msg != "media not found" {
			t.Errorf("message = %q, want %q", msg, "media not found")
		}
	})
}

// TestExternalIDTableWinsWhenBothSourcesDisagree pins the precedence: the
// lookup table is the ingest's own record of the mapping, so it answers before
// the row column does. A refactor that reads the column first would change which
// row a URL points at without changing any error message.
func TestExternalIDTableWinsWhenBothSourcesDisagree(t *testing.T) {
	db := setupShowsDB(t)

	fromTable := database.Show{ID: 11, Title: "Table Show", TmdbID: 222}
	fromColumn := database.Show{ID: 12, Title: "Column Show", TmdbID: 222}
	for _, show := range []database.Show{fromTable, fromColumn} {
		s := show
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("seed show %q: %v", s.Title, err)
		}
	}
	if err := db.Create(&database.ExternalID{
		MediaType: "show", MediaID: fromTable.ID, Source: "tmdb", ExternalID: "222",
	}).Error; err != nil {
		t.Fatalf("seed external id: %v", err)
	}

	resp := scopeGet(t, scopeApp(), "/v1/shows/222?id_type=tmdb")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/shows/222?id_type=tmdb = %d (%s), want 200", resp.StatusCode, errorMessage(t, resp))
	}
	if got := titleOf(t, resp); got != "Table Show" {
		t.Errorf("resolved to %q, want Table Show (the external_ids row)", got)
	}
}

// TestStaleExternalIDFallsBackToTheColumn covers an external_ids row left behind
// by a deleted media row: it must not resolve to a phantom, and it must not hide
// a live row that carries the same id.
func TestStaleExternalIDFallsBackToTheColumn(t *testing.T) {
	db := setupShowsDB(t)

	live := database.Show{ID: 21, Title: "Live Show", TmdbID: 333}
	if err := db.Create(&live).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}
	// Points at row 999, which does not exist.
	if err := db.Create(&database.ExternalID{
		MediaType: "show", MediaID: 999, Source: "tmdb", ExternalID: "333",
	}).Error; err != nil {
		t.Fatalf("seed stale external id: %v", err)
	}

	resp := scopeGet(t, scopeApp(), "/v1/shows/333?id_type=tmdb")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/shows/333?id_type=tmdb = %d (%s), want 200", resp.StatusCode, errorMessage(t, resp))
	}
	if got := titleOf(t, resp); got != "Live Show" {
		t.Errorf("resolved to %q, want Live Show", got)
	}
}

// TestZeroExternalIDIsNotARow pins the trap in the column read: 0 is what the
// media tables store for an id they do not hold, so querying `tvdb_id = 0` (or
// `tmdb_id = 0`) matches every row that has no such id and answers a request for
// id 0 with an unrelated title.
func TestZeroExternalIDIsNotARow(t *testing.T) {
	db := setupShowsDB(t)

	// A row ingested from TVDB only: no TMDB id, no IMDb id, no slug.
	if err := db.Create(&database.Show{ID: 31, Title: "TVDB Only"}).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}
	// A row with a TMDB id but no TVDB id.
	if err := db.Create(&database.Show{ID: 32, Title: "TMDB Only", TmdbID: 4242}).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}
	app := scopeApp()

	for _, query := range []string{"?id_type=tmdb", "?id_type=tvdb", "?id_type=imdb"} {
		resp := scopeGet(t, app, "/v1/shows/0"+query)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET /v1/shows/0%s = %d (%s), want 404", query, resp.StatusCode, errorMessage(t, resp))
			continue
		}
		if msg := errorMessage(t, resp); msg != "media not found" {
			t.Errorf("GET /v1/shows/0%s message = %q, want %q", query, msg, "media not found")
		}
	}
}

// TestOutOfRangeExternalIDIsAMiss: a number that cannot fit the column is an id
// no row can hold, so it is a 404. It used to be a 500 "database query error" —
// the parse error was reported as if a query had failed.
func TestOutOfRangeExternalIDIsAMiss(t *testing.T) {
	db := setupShowsDB(t)
	if err := db.Create(&database.Show{ID: 41, Title: "Any Show", TmdbID: 4242}).Error; err != nil {
		t.Fatalf("seed show: %v", err)
	}

	resp := scopeGet(t, scopeApp(), "/v1/shows/9223372036854775808?id_type=tmdb")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /v1/shows/9223372036854775808?id_type=tmdb = %d, want 404", resp.StatusCode)
	}
	if msg := errorMessage(t, resp); msg != "media not found" {
		t.Errorf("message = %q, want %q", msg, "media not found")
	}
}
