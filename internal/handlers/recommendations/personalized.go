package recommendations

import (
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/metadata"
	"github.com/haaku-tv/haaku/internal/recommend"
	"github.com/haaku-tv/haaku/internal/response"
)

// recommendationsFor merges the provider backbone with the catalogue engine.
//
// Order matters: the provider's picks lead, because on an instance nobody has
// watched anything yet they are the only real signal. The engine's own ranking
// follows, minus anything already picked. An instance with no token gets the
// engine alone, and an account with no history still gets the popular shelf.
func recommendationsFor(c *fiber.Ctx, db *gorm.DB, userID, kind string, limit int) ([]recommend.Item, bool, error) {
	engineItems, personalised, err := recommend.Personal(db, userID, kind, limit)
	if err != nil {
		return nil, false, err
	}

	resolver := metadata.DefaultResolver()
	if resolver == nil {
		return engineItems, personalised, nil
	}

	seeds, err := recommend.Seeds(db, userID, kind, 5)
	if err != nil {
		// A missing provider id is not a failure of this endpoint: the catalogue
		// ranking above already answered.
		slog.Debug("[recommend] could not read seed ids", "kind", kind, "error", err)
	}
	if len(seeds) == 0 {
		return engineItems, personalised, nil
	}

	providerSeeds := make([]metadata.RecommendationSeed, 0, len(seeds))
	for _, seed := range seeds {
		providerSeeds = append(providerSeeds, metadata.RecommendationSeed{
			Kind: kind, TmdbID: seed.TmdbID, Weight: seed.Weight,
		})
	}

	var tmdbIDs []int
	if ids, provErr := resolver.ProviderRecommendations(c.UserContext(), providerSeeds, limit*2); provErr == nil {
		tmdbIDs = ids
	} else {
		// The backbone is an enhancement; a provider outage leaves the catalogue
		// ranking in place rather than failing the row.
		slog.Debug("[recommend] provider recommendations failed", "kind", kind, "error", provErr)
	}
	if len(tmdbIDs) == 0 {
		return engineItems, personalised, nil
	}

	var localIDs []int64
	if ids, mapErr := resolver.LocalIDsForTmdb(c.UserContext(), kind, tmdbIDs); mapErr == nil {
		localIDs = ids
	} else {
		slog.Debug("[recommend] could not map provider ids", "kind", kind, "error", mapErr)
	}
	if len(localIDs) == 0 {
		return engineItems, personalised, nil
	}

	var seen map[int64]struct{}
	if ids, seenErr := recommend.SeenIDs(db, userID, kind); seenErr == nil {
		seen = ids
	} else {
		slog.Debug("[recommend] could not read the seen set", "kind", kind, "error", seenErr)
	}

	items := make([]recommend.Item, 0, limit)
	taken := make(map[int64]struct{}, limit)
	for _, id := range localIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, ok := taken[id]; ok {
			continue
		}
		taken[id] = struct{}{}
		items = append(items, recommend.Item{Kind: kind, ID: id, Score: 1})
		if len(items) == limit {
			return items, true, nil
		}
	}

	for _, item := range engineItems {
		if _, ok := taken[item.ID]; ok {
			continue
		}
		taken[item.ID] = struct{}{}
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	return items, personalised || len(items) > 0, nil
}

// moviePage renders one page of ranked movies.
//
// The genre filter runs before the page window — a matching title ranked 21st
// still belongs on page one of a Comedy request — and the count it returns is
// the size of the filtered set, so `row_count` stays constant across pages
// instead of reporting the size of the prefix this call happened to fetch.
func moviePage(rows []database.Movie, ranked map[int64]int, genreFilter string, page, limit int) ([]recMovieItem, int) {
	ordered := orderMovies(rows, ranked)
	matching := make([]database.Movie, 0, len(ordered))
	for _, m := range ordered {
		if matchesGenre(m.Genres, genreFilter) {
			matching = append(matching, m)
		}
	}

	total := len(matching)
	matching = pageWindow(matching, page, limit)

	results := make([]recMovieItem, 0, len(matching))
	for _, m := range matching {
		results = append(results, recMovieItem{
			ID:         m.ID,
			TmdbID:     m.TmdbID,
			Title:      m.Title,
			Overview:   m.Overview,
			Popularity: m.Popularity,
			Genres:     m.Genres.Values(),
			Released:   m.Released.String(),
			Runtime:    m.Runtime,
			RatingAvg:  m.RatingAvg,
			Poster:     m.Images.StringValue("poster"),
		})
	}
	return results, total
}

// showPage is moviePage for shows.
func showPage(rows []database.Show, ranked map[int64]int, genreFilter string, page, limit int) ([]recShowItem, int) {
	ordered := orderShows(rows, ranked)
	matching := make([]database.Show, 0, len(ordered))
	for _, s := range ordered {
		if matchesGenre(s.Genres, genreFilter) {
			matching = append(matching, s)
		}
	}

	total := len(matching)
	matching = pageWindow(matching, page, limit)

	results := make([]recShowItem, 0, len(matching))
	for _, s := range matching {
		results = append(results, recShowItem{
			ID:           s.ID,
			TmdbID:       s.TmdbID,
			Title:        s.Title,
			Overview:     s.Overview,
			Popularity:   s.Popularity,
			Genres:       s.Genres.Values(),
			FirstAired:   s.FirstAired.String(),
			Status:       s.Status,
			EpisodeCount: s.EpisodeCount,
			RatingAvg:    s.RatingAvg,
			Poster:       s.Images.StringValue("poster"),
		})
	}
	return results, total
}

// pageWindow slices one page out of the filtered set.
func pageWindow[T any](rows []T, page, limit int) []T {
	if limit <= 0 {
		return rows
	}
	start := (page - 1) * limit
	if start >= len(rows) {
		return nil
	}
	end := start + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

func orderMovies(rows []database.Movie, ranked map[int64]int) []database.Movie {
	sort.SliceStable(rows, func(i, j int) bool { return ranked[rows[i].ID] < ranked[rows[j].ID] })
	return rows
}

func orderShows(rows []database.Show, ranked map[int64]int) []database.Show {
	sort.SliceStable(rows, func(i, j int) bool { return ranked[rows[i].ID] < ranked[rows[j].ID] })
	return rows
}

// matchesGenre keeps a row whose genres carry the requested name.
func matchesGenre(genres database.StringSlice, wanted string) bool {
	wanted = strings.TrimSpace(wanted)
	if wanted == "" {
		return true
	}
	for _, genre := range genres {
		if strings.EqualFold(strings.TrimSpace(genre), wanted) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Dismissing a recommendation
// ---------------------------------------------------------------------------

// handleHideMovieRecommendation serves DELETE /v1/recommendations/movies/{id}.
//
// Dismissing a row is stored as a hidden entry in the "recommendations"
// section — the same table the calendar and progress feeds read — so the engine
// stops offering the title without the account losing it from its history.
func handleHideMovieRecommendation(c *fiber.Ctx) error {
	return hideRecommendation(c, "movie")
}

// handleHideShowRecommendation serves DELETE /v1/recommendations/shows/{id}.
func handleHideShowRecommendation(c *fiber.Ctx) error {
	return hideRecommendation(c, "show")
}

func hideRecommendation(c *fiber.Ctx, kind string) error {
	userID := c.Locals("user_id").(string)

	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return response.BadRequest(c, "id must be a positive integer")
	}

	db, err := database.GetDB()
	if err != nil {
		return response.InternalError(c)
	}

	hidden := database.UserHidden{
		UserID:    userID,
		Section:   recommend.HiddenSection,
		MediaType: kind,
		MediaID:   id,
		HiddenAt:  time.Now().UTC(),
	}
	// Dismissing twice is not an error: the account's intent is the same.
	if err := db.Where(database.UserHidden{
		UserID: userID, Section: recommend.HiddenSection, MediaType: kind, MediaID: id,
	}).FirstOrCreate(&hidden).Error; err != nil {
		return response.InternalError(c)
	}

	// Hiding is a state change, not a document; 204 keeps clients from parsing a
	// body they do not need.
	return c.SendStatus(fiber.StatusNoContent)
}
