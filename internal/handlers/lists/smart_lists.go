package lists

// Smart lists are their own file: the filter DSL, its two query builders and
// the resolved-items handler are ~500 lines of unrelated machinery, and the
// size ratchet means the list CRUD surface cannot absorb new work while they
// share a file.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/haaku-tv/haaku/internal/cache"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// SMART LISTS (T4.2)
// ============================================================================

type smartListRequest struct {
	Name    string                 `json:"name"`
	Filters map[string]interface{} `json:"filters"`
}

func handleGetSmartLists(c *fiber.Ctx) error {
	uid := userID(c)

	db, err := database.GetDB()
	if err != nil {
		return response.InternalError(c)
	}

	var lists []database.SmartList
	if err := db.Where("user_id = ?", uid).Order("created_at DESC").Find(&lists).Error; err != nil {
		return response.InternalError(c)
	}
	if lists == nil {
		lists = make([]database.SmartList, 0)
	}

	return c.JSON(lists)
}

func handleCreateSmartList(c *fiber.Ctx) error {
	uid := userID(c)

	var req smartListRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return response.BadRequest(c, "name is required")
	}

	sl := database.SmartList{
		UserID: uid,
		Name:   strings.TrimSpace(req.Name),
	}

	if req.Filters != nil {
		sl.Filters = database.JSONMap(req.Filters)
	} else {
		sl.Filters = database.JSONMap{}
	}

	err := cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := tx.Create(&sl).Error; err != nil {
			return nil, err
		}
		bumpUserVersion(cacheTx, uid)
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.Status(fiber.StatusCreated).JSON(sl)
}

func handleGetSmartList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	slID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid smart list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var sl database.SmartList
	if err := db.First(&sl, slID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "smart list not found")
		}
		return response.InternalError(c)
	}

	if sl.UserID != uid {
		return response.Forbidden(c, "you do not have access to this smart list")
	}

	return c.JSON(sl)
}

func handleUpdateSmartList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	slID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid smart list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var sl database.SmartList
	if err := db.First(&sl, slID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "smart list not found")
		}
		return response.InternalError(c)
	}

	if sl.UserID != uid {
		return response.Forbidden(c, "you do not have access to this smart list")
	}

	var req smartListRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}

	updates := map[string]interface{}{}
	if req.Name != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if req.Filters != nil {
		updates["filters"] = database.JSONMap(req.Filters)
	}

	if len(updates) == 0 {
		return response.BadRequest(c, "no fields to update")
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := tx.Model(&sl).Updates(updates).Error; err != nil {
			return nil, err
		}
		bumpUserVersion(cacheTx, uid)
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	db.First(&sl, slID)
	return c.JSON(sl)
}

func handleDeleteSmartList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	slID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid smart list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var sl database.SmartList
	if err := db.First(&sl, slID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "smart list not found")
		}
		return response.InternalError(c)
	}

	if sl.UserID != uid {
		return response.Forbidden(c, "you do not have access to this smart list")
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := tx.Delete(&sl).Error; err != nil {
			return nil, err
		}
		bumpUserVersion(cacheTx, uid)
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// handleGetSmartListItems resolves the smart list filter DSL dynamically.
func handleGetSmartListItems(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	slID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid smart list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var sl database.SmartList
	if err := db.First(&sl, slID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "smart list not found")
		}
		return response.InternalError(c)
	}

	if sl.UserID != uid {
		return response.Forbidden(c, "you do not have access to this smart list")
	}

	mediaTypeFilter := c.Query("type", "")
	sortBy := c.Query("sort_by", "popularity")
	page, limit := response.ParsePagination(c, 25)

	// Build query from filter DSL
	results, total, err := resolveSmartListQuery(db, sl.Filters, uid, mediaTypeFilter, sortBy, page, limit)
	if err != nil {
		return response.InternalError(c)
	}

	return c.JSON(fiber.Map{
		"items":     results,
		"page":      page,
		"limit":     limit,
		"row_count": total,
	})
}

// resolveSmartListQuery builds a GORM query from the filter DSL.
func resolveSmartListQuery(db *gorm.DB, filters database.JSONMap, userID, mediaTypeFilter, sortBy string, page, limit int) ([]map[string]interface{}, int64, error) {
	// Determine which media types to query.
	allowedTypes := []string{"movie", "show"}
	if rawTypes, ok := filters["type"]; ok {
		if typeArr, ok := rawTypes.([]interface{}); ok {
			allowedTypes = nil
			for _, t := range typeArr {
				if ts, ok := t.(string); ok {
					allowedTypes = append(allowedTypes, ts)
				}
			}
		}
	}

	// If a specific type is requested in the query, intersect
	if mediaTypeFilter != "" {
		found := false
		for _, t := range allowedTypes {
			if t == mediaTypeFilter {
				found = true
				break
			}
		}
		if found {
			allowedTypes = []string{mediaTypeFilter}
		} else {
			return []map[string]interface{}{}, 0, nil
		}
	}

	var allResults []map[string]interface{}
	var totalCount int64

	for _, mediaType := range allowedTypes {
		switch mediaType {
		case "movie":
			query := db.Table("movies")
			query = applyFilterDSL(query, filters, "movies")
			query = applyUserFilters(query, filters, userID, "movie")
			var count int64
			query.Count(&count)
			totalCount += count

			var movies []database.Movie
			q := db.Table("movies")
			q = applyFilterDSL(q, filters, "movies")
			q = applyUserFilters(q, filters, userID, "movie")

			// Fetch only the rows the requested page of the COMBINED set can
			// reach: every row up to the end of this page, per type, with no
			// per-type OFFSET (the page is sliced from the combined result, so
			// each type has to contribute its top page*limit rows).
			if err := q.Order(fmt.Sprintf("%s DESC, id ASC", sanitizeSortColumn(sortBy))).
				Limit(page * limit).
				Find(&movies).Error; err != nil {
				return nil, 0, err
			}
			for _, m := range movies {
				year := 0
				if m.Released != nil {
					year = m.Released.Year()
				}
				allResults = append(allResults, map[string]interface{}{
					"type":     "movie",
					"movie_id": m.ID,
					"title":    m.Title,
					"year":     year,
					"rating":   m.RatingAvg,
				})
			}
		case "show":
			query := db.Table("shows")
			query = applyFilterDSL(query, filters, "shows")
			query = applyUserFilters(query, filters, userID, "show")
			var count int64
			query.Count(&count)
			totalCount += count

			var shows []database.Show
			q := db.Table("shows")
			q = applyFilterDSL(q, filters, "shows")
			q = applyUserFilters(q, filters, userID, "show")

			if err := q.Order(fmt.Sprintf("%s DESC, id ASC", sanitizeSortColumn(sortBy))).
				Limit(page * limit).
				Find(&shows).Error; err != nil {
				return nil, 0, err
			}
			for _, s := range shows {
				year := 0
				if s.FirstAired != nil {
					year = s.FirstAired.Year()
				}
				allResults = append(allResults, map[string]interface{}{
					"type":    "show",
					"show_id": s.ID,
					"title":   s.Title,
					"year":    year,
					"rating":  s.RatingAvg,
				})
			}
		}
	}

	if allResults == nil {
		allResults = make([]map[string]interface{}, 0)
	}

	// Page over the COMBINED result set (not per type). Without this, each
	// media type was OFFSET/LIMIT-ed independently and then concatenated, so
	// later pages repeated/dropped items. totalCount (summed per type above)
	// is the full combined size, so the page slice stays consistent with it.
	start := (page - 1) * limit
	if start < 0 {
		start = 0
	}
	if start >= len(allResults) {
		return []map[string]interface{}{}, totalCount, nil
	}
	end := start + limit
	if end > len(allResults) {
		end = len(allResults)
	}
	paged := make([]map[string]interface{}, end-start)
	copy(paged, allResults[start:end])

	return paged, totalCount, nil
}

// applyFilterDSL applies the core filter DSL to a GORM query.
func applyFilterDSL(query *gorm.DB, filters database.JSONMap, tableAlias string) *gorm.DB {
	// Genre filter
	if rawGenres, ok := filters["genres"]; ok {
		if genres, ok := rawGenres.([]interface{}); ok && len(genres) > 0 {
			for _, g := range genres {
				if gs, ok := g.(string); ok {
					query = query.Where(fmt.Sprintf("%s.genres::jsonb ? ?", tableAlias), gs)
				}
			}
		}
	}

	// Year range filter
	if rawYear, ok := filters["year"]; ok {
		if yearStr, ok := rawYear.(string); ok {
			parts := strings.SplitN(yearStr, "-", 2)
			if len(parts) == 2 {
				startYear := parts[0]
				endYear := parts[1]
				if tableAlias == "movies" {
					if startYear != "" {
						query = query.Where(fmt.Sprintf("EXTRACT(YEAR FROM %s.released) >= ?", tableAlias), startYear)
					}
					if endYear != "" {
						query = query.Where(fmt.Sprintf("EXTRACT(YEAR FROM %s.released) <= ?", tableAlias), endYear)
					}
				} else {
					if startYear != "" {
						query = query.Where(fmt.Sprintf("EXTRACT(YEAR FROM %s.first_aired) >= ?", tableAlias), startYear)
					}
					if endYear != "" {
						query = query.Where(fmt.Sprintf("EXTRACT(YEAR FROM %s.first_aired) <= ?", tableAlias), endYear)
					}
				}
			} else if yearStr != "" {
				if tableAlias == "movies" {
					query = query.Where(fmt.Sprintf("EXTRACT(YEAR FROM %s.released) = ?", tableAlias), yearStr)
				} else {
					query = query.Where(fmt.Sprintf("EXTRACT(YEAR FROM %s.first_aired) = ?", tableAlias), yearStr)
				}
			}
		}
	}

	// Rating minimum
	if rawRating, ok := filters["rating_min"]; ok {
		if rating, ok := toFloat64(rawRating); ok {
			query = query.Where(fmt.Sprintf("%s.rating_avg >= ?", tableAlias), rating)
		}
	}

	// Vote count minimum
	if rawVotes, ok := filters["votes_min"]; ok {
		if votes, ok := toFloat64(rawVotes); ok {
			query = query.Where(fmt.Sprintf("%s.rating_count >= ?", tableAlias), int64(votes))
		}
	}

	// Status filter (shows only)
	if rawStatus, ok := filters["status"]; ok {
		if statuses, ok := rawStatus.([]interface{}); ok && len(statuses) > 0 && tableAlias == "shows" {
			statusStrs := make([]string, 0, len(statuses))
			for _, s := range statuses {
				if ss, ok := s.(string); ok {
					statusStrs = append(statusStrs, ss)
				}
			}
			if len(statusStrs) > 0 {
				query = query.Where(fmt.Sprintf("%s.status IN ?", tableAlias), statusStrs)
			}
		}
	}

	return query
}

// applyUserFilters applies watched/collected filters against user_* tables.
// userFilterTables maps a media type to the table the outer query selects, which
// the EXISTS subqueries must reference. Using the TYPE ("movie") instead of the
// table ("movies") produced a correlated reference to a table that is not in the
// FROM list, so every watched/collected filter errored on PostgreSQL.
var userFilterTables = map[string]string{"movie": "movies", "show": "shows"}

func applyUserFilters(query *gorm.DB, filters database.JSONMap, userID, mediaType string) *gorm.DB {
	table, ok := userFilterTables[mediaType]
	if !ok {
		return query
	}

	// Watched filter
	if rawWatched, ok := filters["watched"]; ok {
		if watched, ok := rawWatched.(string); ok {
			subQuery := fmt.Sprintf(
				"SELECT 1 FROM user_history WHERE user_history.user_id = ? AND user_history.media_type = ? AND user_history.media_id = %s.id", table)
			switch watched {
			case "all":
				query = query.Where(fmt.Sprintf("EXISTS (%s)", subQuery), userID, mediaType)
			case "none":
				query = query.Where(fmt.Sprintf("NOT EXISTS (%s)", subQuery), userID, mediaType)
			}
		}
	}

	// Collected filter
	if rawCollected, ok := filters["collected"]; ok {
		if collected, ok := rawCollected.(string); ok {
			subQuery := fmt.Sprintf(
				"SELECT 1 FROM user_collection WHERE user_collection.user_id = ? AND user_collection.media_type = ? AND user_collection.media_id = %s.id", table)
			switch collected {
			case "all":
				query = query.Where(fmt.Sprintf("EXISTS (%s)", subQuery), userID, mediaType)
			case "none":
				query = query.Where(fmt.Sprintf("NOT EXISTS (%s)", subQuery), userID, mediaType)
			}
		}
	}

	return query
}

func sanitizeSortColumn(col string) string {
	allowed := map[string]bool{
		"popularity":   true,
		"rating_avg":   true,
		"rating_count": true,
		"released":     true,
		"first_aired":  true,
		"created_at":   true,
		"title":        true,
	}
	if allowed[col] {
		return col
	}
	return "popularity"
}

// ============================================================================
// HELPERS
// ============================================================================

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case json.Number:
		f, err := val.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
