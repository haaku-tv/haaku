package media

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
)

// ResolveRouteID is the quiet sibling of ResolveIDAs: it reports what a route id
// IS (including an episode or a season, which are local row ids the type-agnostic
// resolver never looks at) instead of writing the 404 itself, so a handler whose
// route answers a wrong-type id with 400 can keep that answer.

// routeIDApp exposes the resolver's verdict as JSON so a test can assert on it
// without going through a handler's wording.
func routeIDApp() *fiber.App {
	app := fiber.New()
	app.Get("/v1/:want/:id", func(c *fiber.Ctx) error {
		id, failure, err := ResolveRouteID(c, c.Params("id"), c.Params("want"))
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"id": id, "failure": int(failure)})
	})
	return app
}

type routeIDVerdict struct {
	ID      int64 `json:"id"`
	Failure int   `json:"failure"`
}

func askRouteID(t *testing.T, app *fiber.App, path string) routeIDVerdict {
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
	}
	var out routeIDVerdict
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

func TestResolveRouteIDAnswersEveryRouteType(t *testing.T) {
	db := setupShowsDB(t)
	// Movie 1, show 1, season 1 and episode 1 all share the number on purpose:
	// every table's sequence starts at 1, which is what the route's type is
	// there to disambiguate.
	for _, model := range []any{
		&database.Movie{ID: 1, Title: "The Matrix", TmdbID: 603, Slug: "the-matrix"},
		&database.Show{ID: 1, Title: "Severance", TmdbID: 95396, Slug: "severance"},
		&database.Season{ID: 1, ShowID: 1, SeasonNumber: 1},
		&database.Episode{ID: 1, ShowID: 1, SeasonID: 1, EpisodeNumber: 1, Title: "Good News About Hell"},
		// Episode 40 has no movie, show or season 40 to collide with.
		&database.Episode{ID: 40, ShowID: 1, SeasonID: 1, EpisodeNumber: 2, Title: "Half Loop"},
	} {
		if err := db.Create(model).Error; err != nil {
			t.Fatalf("seed %T: %v", model, err)
		}
	}
	app := routeIDApp()

	cases := []struct {
		path        string
		wantID      int64
		wantFailure Failure
	}{
		// The route's type is authoritative, so the same number resolves in each
		// table that holds it.
		{"/v1/movie/1", 1, FailureNone},
		{"/v1/show/1", 1, FailureNone},
		{"/v1/season/1", 1, FailureNone},
		{"/v1/episode/1", 1, FailureNone},
		// ... including a number several tables hold.
		{"/v1/episode/40", 40, FailureNone},
		// The slug and external-id sources still address a movie or a show.
		{"/v1/show/severance", 1, FailureNone},
		{"/v1/movie/603?id_type=tmdb", 1, FailureNone},
		// A slug is not an episode, and a missing row is missing, not wrong.
		{"/v1/episode/severance", 0, FailureMissing},
		{"/v1/episode/9999", 0, FailureMissing},
		// An id another type holds is the caller's wrong-type case.
		{"/v1/movie/40", 0, FailureWrongType},
		{"/v1/season/40", 0, FailureWrongType},
		{"/v1/movie/95396?id_type=tmdb", 0, FailureWrongType},
		{"/v1/movie/nope?id_type=rot13", 0, FailureBadIDType},
	}
	for _, tc := range cases {
		got := askRouteID(t, app, tc.path)
		if got.Failure != int(tc.wantFailure) {
			t.Errorf("GET %s: failure = %d, want %d", tc.path, got.Failure, tc.wantFailure)
			continue
		}
		if got.ID != tc.wantID {
			t.Errorf("GET %s: id = %d, want %d", tc.path, got.ID, tc.wantID)
		}
	}
}
