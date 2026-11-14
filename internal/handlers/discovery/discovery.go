package discovery

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"strconv"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/handlers/media"
	"github.com/haaku-tv/haaku/internal/metadata"
	"github.com/haaku-tv/haaku/internal/openapi"
	"github.com/haaku-tv/haaku/internal/recommend"
	projredis "github.com/haaku-tv/haaku/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/haaku-tv/haaku/internal/response"
	"gorm.io/gorm"
)

func init() {
	registerOpenAPIRoutes()
}

// registerOpenAPIRoutes registers all discovery endpoints in the OpenAPI registry.
func registerOpenAPIRoutes() {
	routes := []openapi.RouteDef{
		// Movies discovery
		{Method: "GET", Path: "/v1/movies/trending", Summary: "Get trending movies", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Trending movies"}},
		{Method: "GET", Path: "/v1/movies/popular", Summary: "Get popular movies", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Popular movies"}},
		{Method: "GET", Path: "/v1/movies/recommendations/{id}", Summary: "Get movies like this one", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Recommendations", 400: "Invalid id"}},
		{Method: "GET", Path: "/v1/movies/{id}/related", Summary: "Get movies related to this one", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Related movies", 400: "Invalid id"}},

		// Shows discovery
		{Method: "GET", Path: "/v1/shows/trending", Summary: "Get trending shows", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Trending shows"}},
		{Method: "GET", Path: "/v1/shows/popular", Summary: "Get popular shows", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Popular shows"}},
		{Method: "GET", Path: "/v1/shows/recommendations/{id}", Summary: "Get shows like this one", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Recommendations", 400: "Invalid id"}},
		{Method: "GET", Path: "/v1/shows/{id}/related", Summary: "Get shows related to this one", Tags: []string{"Discovery"}, Auth: "none", StatusCodes: map[int]string{200: "Related shows", 400: "Invalid id"}},
	}

	for _, r := range routes {
		openapi.Register(r)
	}
}

// RegisterRoutes wires all discovery route handlers to the Fiber router.
func RegisterRoutes(api fiber.Router) {
	// --- Movies ---
	api.Get("/movies/trending", handleMoviesTrending)
	api.Get("/movies/popular", handleMoviesPopular)
	api.Get("/movies/recommendations/:id", handleMoviesRecommendations)
	// Trakt's own surface for the same question; registered after the
	// recommendations path so the two static-ish segments cannot shadow each
	// other.
	api.Get("/movies/:id/related", handleMoviesRecommendations)

	// --- Shows ---
	api.Get("/shows/trending", handleShowsTrending)
	api.Get("/shows/popular", handleShowsPopular)
	api.Get("/shows/recommendations/:id", handleShowsRecommendations)
	api.Get("/shows/:id/related", handleShowsRecommendations)

	// Contract-shaped anticipated feeds — static "/anticipated" must precede
	// media's parameterised /movies/:id and /shows/:id.
	registerContractAnticipatedRoutes(api)

	// Period rankings, hot, and the merged cross-type feeds (issue #66). Every
	// static segment here (watched, played, collected, favorited, hot, media)
	// must be registered before media's parameterised /movies/:id and
	// /shows/:id routes.
	registerRankingRoutes(api)
	registerMediaRankingRoutes(api)
}

// ---------------------------------------------------------------------------
// Movies — Trending / Popular
// ---------------------------------------------------------------------------

func handleMoviesTrending(c *fiber.Ctx) error {
	return moviesList(c, "trending")
}

func handleMoviesPopular(c *fiber.Ctx) error {
	return moviesList(c, "popular")
}

func handleShowsTrending(c *fiber.Ctx) error {
	return showsList(c, "trending")
}

func handleShowsPopular(c *fiber.Ctx) error {
	return showsList(c, "popular")
}

// moviesList queries movies ordered by popularity DESC with pagination.
// Results are cached in Redis (short TTL) because these are hot, public,
// read-heavy endpoints. A cache failure never fails the request — it falls
// through to the DB.
func moviesList(c *fiber.Ctx, kind string) error {
	page, limit := response.ParsePagination(c, 20)
	extended := media.GetExtended(c)

	cacheKey := discoveryCacheKey("movies", kind, page, limit, extended)
	if data, ok := discoveryGetCache(cacheKey); ok {
		c.Set("Content-Type", "application/json")
		return c.Send(data)
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c, "database unavailable")
	}

	var movies []database.Movie
	if err := db.Order("popularity DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&movies).Error; err != nil {
		return response.InternalError(c, "database query error")
	}

	resp := make([]media.MovieResponse, 0, len(movies))
	for i := range movies {
		resp = append(resp, media.BuildMovieResponse(&movies[i], extended))
	}

	discoverySetCache(cacheKey, resp)
	return c.JSON(resp)
}

// showsList queries shows ordered by popularity DESC with pagination.
func showsList(c *fiber.Ctx, kind string) error {
	page, limit := response.ParsePagination(c, 20)
	extended := media.GetExtended(c)

	cacheKey := discoveryCacheKey("shows", kind, page, limit, extended)
	if data, ok := discoveryGetCache(cacheKey); ok {
		c.Set("Content-Type", "application/json")
		return c.Send(data)
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c, "database unavailable")
	}

	var shows []database.Show
	if err := db.Order("popularity DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&shows).Error; err != nil {
		return response.InternalError(c, "database query error")
	}

	resp := make([]media.ShowResponse, 0, len(shows))
	for i := range shows {
		resp = append(resp, media.BuildShowResponse(&shows[i], extended))
	}

	discoverySetCache(cacheKey, resp)
	return c.JSON(resp)
}

// ---------------------------------------------------------------------------
// Recommendations — blocked on the recommendations engine (issue #5)
// ---------------------------------------------------------------------------

func handleMoviesRecommendations(c *fiber.Ctx) error {
	return relatedMedia(c, "movie")
}

func handleShowsRecommendations(c *fiber.Ctx) error {
	return relatedMedia(c, "show")
}

// relatedItems answers "what else is like this title".
//
// The metadata provider leads when it is configured: TMDB's own related list is
// the graph a single-user instance has no way to learn, and it is the same list
// the title's TMDB page shows. The catalogue engine follows with what it can
// score locally, and carries the whole answer when there is no token.
func relatedItems(c *fiber.Ctx, db *gorm.DB, kind string, id int64, limit int) ([]recommend.Item, error) {
	engineItems, err := recommend.Related(db, kind, id, limit*2)
	if err != nil {
		return nil, err
	}

	resolver := metadata.DefaultResolver()
	if resolver == nil {
		return trim(engineItems, limit), nil
	}

	var tmdbID int
	table := "movies"
	if kind == "show" {
		table = "shows"
	}
	if scanErr := db.Table(table).Select("tmdb_id").Where("id = ?", id).Scan(&tmdbID).Error; scanErr != nil {
		slog.Debug("[recommend] could not read the provider id", "kind", kind, "id", id, "error", scanErr)
	}
	if tmdbID <= 0 {
		return trim(engineItems, limit), nil
	}

	var tmdbIDs []int
	if ids, provErr := resolver.ProviderRecommendations(c.UserContext(), []metadata.RecommendationSeed{
		{Kind: kind, TmdbID: tmdbID, Weight: 1},
	}, limit*2); provErr == nil {
		tmdbIDs = ids
	} else {
		slog.Debug("[recommend] provider related failed", "kind", kind, "id", id, "error", provErr)
	}
	if len(tmdbIDs) == 0 {
		return trim(engineItems, limit), nil
	}

	var localIDs []int64
	if ids, mapErr := resolver.LocalIDsForTmdb(c.UserContext(), kind, tmdbIDs); mapErr == nil {
		localIDs = ids
	} else {
		slog.Debug("[recommend] could not map provider ids", "kind", kind, "error", mapErr)
	}
	if len(localIDs) == 0 {
		return trim(engineItems, limit), nil
	}

	items := make([]recommend.Item, 0, limit)
	taken := make(map[int64]struct{}, limit)
	taken[id] = struct{}{}
	for _, localID := range localIDs {
		if _, ok := taken[localID]; ok {
			continue
		}
		taken[localID] = struct{}{}
		items = append(items, recommend.Item{Kind: kind, ID: localID, Score: 1})
		if len(items) == limit {
			return items, nil
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
	return items, nil
}

func trim(items []recommend.Item, limit int) []recommend.Item {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

// relatedMedia answers "what else is like this title" from the catalogue.
//
// The same ranking backs Trakt's /movies/{id}/related and our own
// /movies/recommendations/{id}: they are the same question, and answering them
// from one engine is what keeps the two surfaces consistent.
func relatedMedia(c *fiber.Ctx, kind string) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return response.BadRequest(c, "id must be a positive integer")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	items, err := relatedItems(c, db, kind, id, limit)
	if err != nil {
		return response.InternalError(c)
	}

	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	if kind == "show" {
		var shows []database.Show
		if len(ids) > 0 {
			if err := db.Where("id IN ?", ids).Find(&shows).Error; err != nil {
				return response.InternalError(c)
			}
		}
		byID := make(map[int64]database.Show, len(shows))
		for _, show := range shows {
			byID[show.ID] = show
		}
		results := make([]media.ShowResponse, 0, len(shows))
		for _, item := range items {
			show, ok := byID[item.ID]
			if !ok {
				continue
			}
			results = append(results, media.BuildShowResponse(&show, "min"))
		}
		return c.JSON(results)
	}

	var movies []database.Movie
	if len(ids) > 0 {
		if err := db.Where("id IN ?", ids).Find(&movies).Error; err != nil {
			return response.InternalError(c)
		}
	}
	byID := make(map[int64]database.Movie, len(movies))
	for _, movie := range movies {
		byID[movie.ID] = movie
	}
	results := make([]media.MovieResponse, 0, len(movies))
	for _, item := range items {
		movie, ok := byID[item.ID]
		if !ok {
			continue
		}
		results = append(results, media.BuildMovieResponse(&movie, "min"))
	}
	return c.JSON(results)
}

// ---------------------------------------------------------------------------
// Redis TTL cache for hot public listings
// ---------------------------------------------------------------------------

// discoveryListTTL bounds how long a hot public listing (trending/popular) is
// served from cache. Short enough to stay fresh, long enough to absorb bursts.
const discoveryListTTL = 5 * time.Minute

// discoveryCacheKey builds a stable key from every input that affects the
// response body: media type, list kind, page, limit, and extended level.
func discoveryCacheKey(mediaType, kind string, page, limit int, extended string) string {
	return fmt.Sprintf("discovery:%s:%s:page%d:limit%d:extended%s", mediaType, kind, page, limit, extended)
}

// cachedListing is what the paginated list endpoints store in Redis: the encoded
// response body PLUS the pagination metadata that is delivered as headers.
//
// Caching only the body meant a cache hit answered without X-Pagination-*, so
// the same request returned different headers depending on whether the entry was
// warm. That is a real client-visible loss, and it made the ranked feeds flap
// between conformant and failing in `make conformance` — the harness re-requests
// a passing payload for its injection probes, so a run could see a cold first
// request (headers present, PASS) and the next run a warm one (no headers,
// FAIL).
type cachedListing struct {
	Body  json.RawMessage `json:"body"`
	Page  int             `json:"page"`
	Limit int             `json:"limit"`
	Total int64           `json:"total"`
	Items int             `json:"items"`
}

// discoveryGetListing replays a cached listing onto the response, headers
// included, and reports whether it answered. A miss, an unreadable entry or any
// Redis problem returns false so the caller falls through to the DB — the cache
// is best-effort and never fails the request.
func discoveryGetListing(c *fiber.Ctx, key string) bool {
	if projredis.GetClient() == nil {
		return false
	}
	data, err := projredis.GetCacheData(key)
	if err != nil {
		return false
	}
	var listing cachedListing
	if err := json.Unmarshal(data, &listing); err != nil || len(listing.Body) == 0 {
		// An entry written by the old body-only shape, or one with no body, is
		// unusable: treat it as a miss so the next request recomputes and
		// rewrites it rather than answering 200 with an empty body.
		return false
	}
	response.SetPaginationHeaders(c, listing.Page, listing.Limit, listing.Total, listing.Items)
	c.Set("Content-Type", "application/json")
	return c.Send(listing.Body) == nil
}

// discoverySetListing stores the body together with its pagination metadata.
// Best-effort: a Redis or marshal failure only logs.
func discoverySetListing(key string, page, limit int, total int64, items int, v interface{}) {
	if projredis.GetClient() == nil {
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("[discovery] cache marshal error for key %s: %v", key, err)
		return
	}
	payload, err := json.Marshal(cachedListing{
		Body: body, Page: page, Limit: limit, Total: total, Items: items,
	})
	if err != nil {
		log.Printf("[discovery] cache envelope error for key %s: %v", key, err)
		return
	}
	if err := projredis.SetCacheData(key, payload, discoveryListTTL); err != nil {
		log.Printf("[discovery] cache set error for key %s: %v", key, err)
	}
}

// discoveryGetCache returns the cached raw JSON body for key. It returns
// (nil, false) on any miss or Redis unavailability so the caller falls through
// to the DB — the cache is best-effort and never blocks or fails the request.
func discoveryGetCache(key string) ([]byte, bool) {
	if projredis.GetClient() == nil {
		return nil, false
	}
	data, err := projredis.GetCacheData(key)
	if err != nil {
		return nil, false
	}
	return data, true
}

// discoverySetCache stores the marshaled JSON of v under key with discoveryListTTL.
// Best-effort: a Redis failure only logs.
func discoverySetCache(key string, v interface{}) {
	if projredis.GetClient() == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[discovery] cache marshal error for key %s: %v", key, err)
		return
	}
	if err := projredis.SetCacheData(key, data, discoveryListTTL); err != nil {
		log.Printf("[discovery] cache set error for key %s: %v", key, err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
