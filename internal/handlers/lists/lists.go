package lists

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/haaku-tv/haaku/internal/cache"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/openapi"
	"github.com/haaku-tv/haaku/internal/outbox"
	"github.com/haaku-tv/haaku/redis"

	"github.com/gofiber/fiber/v2"
	"github.com/haaku-tv/haaku/internal/auth"
	"github.com/haaku-tv/haaku/internal/response"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	registerOpenAPIRoutes()
}

func registerOpenAPIRoutes() {
	routes := []openapi.RouteDef{
		// Lists
		{Method: "GET", Path: "/v1/users/{id}/lists", Summary: "Get a user's lists", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Lists", 401: "Unauthorized", 403: "Private profile"}},
		{Method: "GET", Path: "/v1/users/me/lists/containing", Summary: "Lists of the caller that contain one media item", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Lists", 400: "Invalid request", 401: "Unauthorized"}},
		{Method: "POST", Path: "/v1/users/me/lists", Summary: "Create a list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{201: "Created list", 400: "Invalid request", 401: "Unauthorized"}},
		{Method: "GET", Path: "/v1/lists/{id}", Summary: "Get a list", Tags: []string{"Lists"}, Auth: "optional", StatusCodes: map[int]string{200: "List", 403: "Private", 404: "Not found"}},
		{Method: "PUT", Path: "/v1/lists/{id}", Summary: "Update a list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Updated list", 400: "Invalid request", 401: "Unauthorized", 403: "Forbidden"}},
		{Method: "DELETE", Path: "/v1/lists/{id}", Summary: "Delete a list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{204: "Deleted", 401: "Unauthorized", 403: "Forbidden"}},
		// List items
		{Method: "GET", Path: "/v1/lists/{id}/items", Summary: "Get list items", Tags: []string{"Lists"}, Auth: "optional", StatusCodes: map[int]string{200: "Items", 403: "Private", 404: "Not found"}},
		{Method: "POST", Path: "/v1/lists/{id}/items", Summary: "Add items to list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{201: "Items added", 400: "Invalid request", 401: "Unauthorized", 403: "Forbidden"}},
		{Method: "POST", Path: "/v1/lists/{id}/items/remove", Summary: "Remove items from list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Items removed", 400: "Invalid request", 401: "Unauthorized", 403: "Forbidden"}},
		{Method: "PUT", Path: "/v1/lists/{id}/items/reorder", Summary: "Reorder list items", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Reordered", 400: "Invalid request", 401: "Unauthorized", 403: "Forbidden"}},
		// Likes
		{Method: "GET", Path: "/v1/lists/{id}/likes", Summary: "Get who liked a list", Tags: []string{"Lists"}, Auth: "optional", StatusCodes: map[int]string{200: "Likes", 404: "Not found"}},
		{Method: "POST", Path: "/v1/lists/{id}/like", Summary: "Like a list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Liked", 401: "Unauthorized", 404: "Not found"}},
		{Method: "DELETE", Path: "/v1/lists/{id}/like", Summary: "Unlike a list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{204: "Unliked", 401: "Unauthorized", 404: "Not found"}},
		// Comments
		{Method: "GET", Path: "/v1/lists/{id}/comments", Summary: "Get list comments", Tags: []string{"Lists"}, Auth: "optional", StatusCodes: map[int]string{200: "Comments", 404: "Not found"}},
		// Popular / Trending
		{Method: "GET", Path: "/v1/lists/popular", Summary: "Get popular lists", Tags: []string{"Lists"}, Auth: "none", StatusCodes: map[int]string{200: "Popular lists"}},
		{Method: "GET", Path: "/v1/lists/trending", Summary: "Get trending lists", Tags: []string{"Lists"}, Auth: "none", StatusCodes: map[int]string{200: "Trending lists"}},
		// Smart lists
		{Method: "GET", Path: "/v1/users/me/smart-lists", Summary: "Get user smart lists", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Smart lists", 401: "Unauthorized"}},
		{Method: "POST", Path: "/v1/users/me/smart-lists", Summary: "Create smart list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{201: "Created", 400: "Invalid request", 401: "Unauthorized"}},
		{Method: "GET", Path: "/v1/smart-lists/{id}", Summary: "Get a smart list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Smart list", 401: "Unauthorized", 404: "Not found"}},
		{Method: "PUT", Path: "/v1/smart-lists/{id}", Summary: "Update smart list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Updated", 400: "Invalid request", 401: "Unauthorized", 403: "Forbidden"}},
		{Method: "DELETE", Path: "/v1/smart-lists/{id}", Summary: "Delete smart list", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{204: "Deleted", 401: "Unauthorized", 403: "Forbidden"}},
		{Method: "GET", Path: "/v1/smart-lists/{id}/items", Summary: "Get resolved smart list items", Tags: []string{"Lists"}, Auth: "required", StatusCodes: map[int]string{200: "Items", 400: "Invalid request", 401: "Unauthorized", 404: "Not found"}},
	}
	for _, r := range routes {
		openapi.Register(r)
	}
}

// RegisterRoutes wires all list route handlers.
//
// Order matters here: Fiber matches routes in registration order, so the static
// /lists/popular and /lists/trending paths must be registered BEFORE /lists/:id.
// Registering them after (as they were) made both unreachable — /lists/:id
// captured "popular" and "trending" as a slug and answered "list not found".
// internal/httpapi's TestNoStaticRouteIsShadowed now guards this class of bug.
func RegisterRoutes(api fiber.Router) {
	// User lists — privacy-gated: AuthRequired then PrivacyFilter("id"); per-list
	api.Get("/users/:id/lists", auth.AuthRequired(), auth.PrivacyFilter("id"), handleGetUserLists)
	api.Post("/users/me/lists", auth.AuthRequired(), handleCreateList)
	// Static path above the list-id routes: "containing" must never be read as
	// a list id.  GET /users/:id/lists answers the same collection shape.
	api.Get("/users/me/lists/containing", auth.AuthRequired(), handleGetListsContaining)

	// Popular / Trending — must precede /lists/:id (see the note above).
	api.Get("/lists/popular", handleGetPopularLists)
	api.Get("/lists/trending", handleGetTrendingLists)

	// Individual list CRUD
	api.Get("/lists/:id", handleGetList)
	api.Put("/lists/:id", auth.AuthRequired(), handleUpdateList)
	api.Delete("/lists/:id", auth.AuthRequired(), handleDeleteList)

	// List items
	api.Get("/lists/:id/items", handleGetListItems)
	api.Post("/lists/:id/items", auth.AuthRequired(), handleAddListItems)
	api.Post("/lists/:id/items/remove", auth.AuthRequired(), handleRemoveListItems)
	api.Put("/lists/:id/items/reorder", auth.AuthRequired(), handleReorderListItems)

	// Likes
	api.Get("/lists/:id/likes", handleGetListLikes)
	api.Post("/lists/:id/like", auth.AuthRequired(), handleLikeList)
	api.Delete("/lists/:id/like", auth.AuthRequired(), handleUnlikeList)

	// Comments
	api.Get("/lists/:id/comments", handleGetListComments)

	// Smart lists
	api.Get("/users/me/smart-lists", auth.AuthRequired(), handleGetSmartLists)
	api.Post("/users/me/smart-lists", auth.AuthRequired(), handleCreateSmartList)
	api.Get("/smart-lists/:id", auth.AuthRequired(), handleGetSmartList)
	api.Put("/smart-lists/:id", auth.AuthRequired(), handleUpdateSmartList)
	api.Delete("/smart-lists/:id", auth.AuthRequired(), handleDeleteSmartList)
	api.Get("/smart-lists/:id/items", auth.AuthRequired(), handleGetSmartListItems)
}

// ============================================================================
// MIDDLEWARE / HELPERS
// ============================================================================

func userID(c *fiber.Ctx) string {
	uid, _ := c.Locals("user_id").(string)
	return uid
}

// bumpListVersion queues a version-counter bump for a list's cached data.
func bumpListVersion(cacheTx *cache.CacheTransaction, listID int64) {
	lvk := redis.BuildListVersionKey(listID)
	cacheTx.AddInvalidation(lvk, map[string]interface{}{
		"list_id":      listID,
		"version_key":  lvk,
		"version_only": true,
	})
}

// bumpUserVersion queues a version-counter bump for a user's cached lists.
func bumpUserVersion(cacheTx *cache.CacheTransaction, userID string) {
	uvk := redis.BuildUserVersionKey(userID)
	cacheTx.AddInvalidation(uvk, map[string]interface{}{
		"user_id":      userID,
		"version_key":  uvk,
		"version_only": true,
	})
}

// ============================================================================
// LIST CRUD
// ============================================================================

// handleGetUserLists returns lists for a user, respecting privacy.
func handleGetUserLists(c *fiber.Ctx) error {
	targetUserID := auth.ResolveTargetID(c, "id") // ":id" accepts the "me" alias
	currentUserID := userID(c)

	db, err := database.GetDB()
	if err != nil {
		return response.InternalError(c)
	}

	query := db.Where("user_id = ?", targetUserID)

	// Privacy filtering: if viewer is not the owner, only show public + link_only lists.
	if currentUserID != targetUserID {
		query = query.Where("privacy IN ?", []string{"public", "link_only"})
	}

	var lists []database.List
	if err := query.Order("sort_order ASC, created_at DESC").Find(&lists).Error; err != nil {
		return response.InternalError(c)
	}

	out, err := contractListObjects(db, lists)
	if err != nil {
		return response.InternalError(c)
	}
	return c.JSON(out)
}

// handleGetListsContaining answers "which of my lists hold this title?" — the
// read the media pages need to show membership and offer removal.
//
// It is a haaku-side extension (the contract has no reverse index for it):
// the lists the caller owns, in any privacy setting, that hold one media item.
// Collaborations are excluded on purpose — item writes are owner-only, so a
// list the caller merely collaborates on could not be toggled from here.
func handleGetListsContaining(c *fiber.Ctx) error {
	uid := userID(c)
	if uid == "" {
		return response.Unauthorized(c)
	}

	mediaType, mediaID, err := listMembershipTarget(c)
	if err != nil {
		return err
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var listIDs []int64
	if err := db.Model(&database.ListItem{}).
		Where("media_type = ? AND media_id = ?", mediaType, mediaID).
		Pluck("list_id", &listIDs).Error; err != nil {
		return response.InternalError(c)
	}
	if len(listIDs) == 0 {
		return c.JSON(make([]map[string]any, 0))
	}

	var lists []database.List
	if err := db.Where("user_id = ? AND id IN ?", uid, listIDs).
		Order("sort_order ASC, created_at DESC").
		Find(&lists).Error; err != nil {
		return response.InternalError(c)
	}

	out, err := contractListObjects(db, lists)
	if err != nil {
		return response.InternalError(c)
	}
	return c.JSON(out)
}

type createListRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Privacy       string `json:"privacy,omitempty"`
	SortBy        string `json:"sort_by,omitempty"`
	SortHow       string `json:"sort_how,omitempty"`
	IsRanked      *bool  `json:"is_ranked,omitempty"`
	CoverURL      string `json:"cover_url,omitempty"`
	AllowComments *bool  `json:"allow_comments,omitempty"`
}

// slugger generates a URL-safe slug from a name.
var slugRegex = regexp.MustCompile(`[^a-z0-9]+`)

func generateSlug(name, userID string) string {
	slug := strings.ToLower(name)
	slug = slugRegex.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "list"
	}
	suffix := userID
	if len(userID) > 8 {
		suffix = userID[:8]
	}
	if suffix == "" {
		return slug
	}
	return fmt.Sprintf("%s-%s", slug, suffix)
}

func handleCreateList(c *fiber.Ctx) error {
	uid := userID(c)

	var req createListRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return response.BadRequest(c, "name is required")
	}

	privacy := "public"
	if req.Privacy != "" {
		privacy = req.Privacy
	}
	if privacy != "public" && privacy != "link_only" && privacy != "private" {
		return response.BadRequest(c, "privacy must be public, link_only, or private")
	}

	allowComments := true
	if req.AllowComments != nil {
		allowComments = *req.AllowComments
	}

	isRanked := false
	if req.IsRanked != nil {
		isRanked = *req.IsRanked
	}

	sortBy := "rank"
	if req.SortBy != "" {
		sortBy = req.SortBy
	}
	sortHow := "asc"
	if req.SortHow != "" {
		sortHow = req.SortHow
	}

	slug := generateSlug(req.Name, uid)

	list := database.List{
		UserID:        uid,
		Name:          strings.TrimSpace(req.Name),
		Description:   req.Description,
		Slug:          slug,
		Privacy:       privacy,
		SortBy:        sortBy,
		SortHow:       sortHow,
		IsRanked:      isRanked,
		CoverURL:      req.CoverURL,
		AllowComments: allowComments,
	}

	err := cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := tx.Create(&list).Error; err != nil {
			// If slug collision, append timestamp
			if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
				list.Slug = fmt.Sprintf("%s-%d", slug, time.Now().Unix())
				if err := tx.Create(&list).Error; err != nil {
					return nil, err
				}
			} else {
				return nil, err
			}
		}

		bumpUserVersion(cacheTx, uid)
		if err := contractPublish(tx, outbox.EventListCreated, uid, map[string]any{
			"list_id": list.ID,
			"slug":    list.Slug,
			"name":    list.Name,
			"privacy": list.Privacy,
		}); err != nil {
			return nil, err
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	// handleCreateList never loaded the db handle: it opened a cache
	// transaction and answered the model it had just written. Re-read it so the
	// response goes through the same contract builder as every other list route.
	createdDB, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}
	obj, objErr := contractListObject(createdDB, &list)
	if objErr != nil {
		return response.InternalError(c)
	}

	return c.Status(fiber.StatusCreated).JSON(obj)
}

// handleGetList returns a single list by ID (or by owner-slug).
// Public endpoint.
func handleGetList(c *fiber.Ctx) error {
	idParam := c.Params("id")

	db, err := database.GetDB()
	if err != nil {
		return response.InternalError(c)
	}

	var list database.List
	// Try numeric ID first, then slug
	if id, parseErr := strconv.ParseInt(idParam, 10, 64); parseErr == nil {
		if err := db.First(&list, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return response.NotFound(c, "list not found")
			}
			return response.InternalError(c)
		}
	} else {
		if err := db.Where("slug = ?", idParam).First(&list).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return response.NotFound(c, "list not found")
			}
			return response.InternalError(c)
		}
	}

	// Privacy check
	currentUserID := userID(c)
	if list.Privacy == "private" && currentUserID != list.UserID {
		return response.Forbidden(c, "this list is private")
	}

	obj, objErr := contractListObject(db, &list)
	if objErr != nil {
		return response.InternalError(c)
	}
	return c.JSON(obj)
}

type updateListRequest struct {
	Name          *string `json:"name,omitempty"`
	Description   *string `json:"description,omitempty"`
	Privacy       *string `json:"privacy,omitempty"`
	SortBy        *string `json:"sort_by,omitempty"`
	SortHow       *string `json:"sort_how,omitempty"`
	IsRanked      *bool   `json:"is_ranked,omitempty"`
	CoverURL      *string `json:"cover_url,omitempty"`
	AllowComments *bool   `json:"allow_comments,omitempty"`
}

func handleUpdateList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, parseErr := strconv.ParseInt(idParam, 10, 64)
	if parseErr != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, err := database.GetDB()
	if err != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	// Ownership check
	if list.UserID != uid {
		return response.Forbidden(c, "you do not have permission to edit this list")
	}

	var req updateListRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}

	updates := map[string]interface{}{}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return response.BadRequest(c, "name cannot be empty")
		}
		updates["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Privacy != nil {
		if *req.Privacy != "public" && *req.Privacy != "link_only" && *req.Privacy != "private" {
			return response.BadRequest(c, "privacy must be public, link_only, or private")
		}
		updates["privacy"] = *req.Privacy
	}
	if req.SortBy != nil {
		updates["sort_by"] = *req.SortBy
	}
	if req.SortHow != nil {
		updates["sort_how"] = *req.SortHow
	}
	if req.IsRanked != nil {
		updates["is_ranked"] = *req.IsRanked
	}
	if req.CoverURL != nil {
		updates["cover_url"] = *req.CoverURL
	}
	if req.AllowComments != nil {
		updates["allow_comments"] = *req.AllowComments
	}

	if len(updates) == 0 {
		return response.BadRequest(c, "no fields to update")
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := advisoryListLock(tx, listID); err != nil {
			return nil, err
		}
		if err := tx.Model(&list).Updates(updates).Error; err != nil {
			return nil, err
		}

		bumpListVersion(cacheTx, listID)
		bumpUserVersion(cacheTx, uid)
		if err := contractPublish(tx, outbox.EventListUpdated, uid, map[string]any{
			"list_id": listID,
			"slug":    list.Slug,
			"name":    list.Name,
			"privacy": list.Privacy,
		}); err != nil {
			return nil, err
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	// Re-fetch
	if err := db.First(&list, listID).Error; err != nil {
		return response.InternalError(c)
	}
	obj, objErr := contractListObject(db, &list)
	if objErr != nil {
		return response.InternalError(c)
	}
	return c.JSON(obj)
}

func handleDeleteList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, parseErr := strconv.ParseInt(idParam, 10, 64)
	if parseErr != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, err := database.GetDB()
	if err != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	if list.UserID != uid {
		return response.Forbidden(c, "you do not have permission to delete this list")
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := advisoryListLock(tx, listID); err != nil {
			return nil, err
		}
		if err := tx.Delete(&list).Error; err != nil {
			return nil, err
		}

		bumpUserVersion(cacheTx, uid)
		if err := contractPublish(tx, outbox.EventListDeleted, uid, map[string]any{
			"list_id": listID,
			"slug":    list.Slug,
			"name":    list.Name,
		}); err != nil {
			return nil, err
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// ============================================================================
// LIST ITEMS
// ============================================================================

// mediaRef is a reference to a media item. The ids it accepts are the same ones
// the API serves: `trakt` is our own row id (which is what the web hands back,
// and what the contract's add-items body uses), then tmdb, then imdb. Declaring
// only `tmdb` is how "Add" from a media page silently added nothing.
type mediaRef struct {
	IDs   contractIDs `json:"ids"`
	Rank  *int        `json:"rank,omitempty"`
	Notes string      `json:"notes,omitempty"`
}

type addItemsRequest struct {
	Movies   []mediaRef `json:"movies,omitempty"`
	Shows    []mediaRef `json:"shows,omitempty"`
	Seasons  []mediaRef `json:"seasons,omitempty"`
	Episodes []mediaRef `json:"episodes,omitempty"`
}

type addItemsResponse struct {
	Added     map[string]int           `json:"added"`
	NotFound  []map[string]interface{} `json:"not_found"`
	Duplicate map[string]int           `json:"duplicate"`
}

type removeItemsRequest struct {
	IDs    []int64    `json:"ids,omitempty"`
	Movies []mediaRef `json:"movies,omitempty"`
	Shows  []mediaRef `json:"shows,omitempty"`
}

type removeItemsResponse struct {
	Removed int `json:"removed"`
}

type reorderRequest struct {
	Order []int64 `json:"order"`
}

func handleGetListItems(c *fiber.Ctx) error {
	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	// Verify list exists and check privacy
	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	currentUserID := userID(c)
	if list.Privacy == "private" && currentUserID != list.UserID {
		return response.Forbidden(c, "this list is private")
	}

	// Parse filters
	mediaType := c.Query("type", "")
	page, limit := response.ParsePagination(c, 25)

	query := db.Where("list_id = ?", listID)
	if mediaType != "" {
		query = query.Where("media_type = ?", mediaType)
	}

	var total int64
	query.Model(&database.ListItem{}).Count(&total)

	// Determine sort order via allowlist — never interpolate user input
	// (List.SortBy) directly into ORDER BY.
	sortCol, sortDir := resolveListItemSort(list.SortBy, list.SortHow)

	var items []database.ListItem
	if err := query.Order(fmt.Sprintf("%s %s", sortCol, sortDir)).
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error; err != nil {
		return response.InternalError(c)
	}

	// The contract's item object: {rank, id, listed_at, type, movie|show|…}.
	// Serving the stored list_items row instead left the client with
	// `media_id` and no nested title, so the poster grid had no name, year or
	// art — and `id` must be the join-row id, which is what remove/reorder take.
	results, err := contractListItemObjects(db, items)
	if err != nil {
		return response.InternalError(c)
	}

	response.SetPaginationHeaders(c, page, limit, total, len(results))

	return c.JSON(results)
}

// listItemSortColumns maps user-facing sort keys to allowlisted SQL columns
// on list_items. Never interpolate user input into ORDER BY directly.
var listItemSortColumns = map[string]string{
	"rank":       "rank",
	"added":      "added_at",
	"media_type": "media_type",
	"media_id":   "media_id",
	"notes":      "notes",
}

// resolveListItemSort maps a user-supplied sort key to an allowlisted SQL
// column and direction. Unknown keys fall back to "rank"; sort_how is
// normalized to ASC/DESC.
func resolveListItemSort(sortBy, sortHow string) (string, string) {
	col, ok := listItemSortColumns[sortBy]
	if !ok {
		col = "rank"
	}
	dir := "ASC"
	if strings.ToUpper(sortHow) == "DESC" {
		dir = "DESC"
	}
	return col, dir
}

// resolveMediaRef turns a list-item reference into our local media id. It
// delegates to resolveContractMedia so the native and contract item writes
// accept exactly the same ids — the media page sends back the `ids` object the
// API served it, which carries `trakt` (our row id) first.
func resolveMediaRef(db *gorm.DB, mediaType string, ref mediaRef) (int64, bool) {
	return resolveContractMedia(db, mediaType, ref.IDs)
}

func handleAddListItems(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	// Only owner can add items
	if list.UserID != uid {
		return response.Forbidden(c, "you do not have permission to modify this list")
	}

	var req addItemsRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}

	result := addItemsResponse{
		Added:     make(map[string]int),
		NotFound:  make([]map[string]interface{}, 0),
		Duplicate: make(map[string]int),
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := advisoryListLock(tx, listID); err != nil {
			return nil, err
		}

		processMediaItems := func(items []mediaRef, mediaType string) {
			for _, ref := range items {
				mediaID, ok := resolveMediaRef(tx, mediaType, ref)
				if !ok {
					result.NotFound = append(result.NotFound, map[string]interface{}{
						"type": mediaType,
						"ids":  ref.IDs,
					})
					continue
				}

				rank := 0
				if ref.Rank != nil {
					rank = *ref.Rank
				}

				item := database.ListItem{
					ListID:    listID,
					MediaType: mediaType,
					MediaID:   mediaID,
					Rank:      rank,
					Notes:     ref.Notes,
					AddedBy:   uid,
				}

				res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
				if res.RowsAffected > 0 {
					result.Added[mediaType]++
				} else {
					result.Duplicate[mediaType]++
				}
			}
		}

		processMediaItems(req.Movies, "movie")
		processMediaItems(req.Shows, "show")
		processMediaItems(req.Seasons, "season")
		processMediaItems(req.Episodes, "episode")

		added := 0
		for _, n := range result.Added {
			added += n
		}
		if added > 0 {
			bumpListVersion(cacheTx, listID)
			if err := contractPublish(tx, outbox.EventListItemsAdded, uid, map[string]any{
				"list_id": listID,
				"items":   result.Added,
			}); err != nil {
				return nil, err
			}
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.Status(fiber.StatusCreated).JSON(result)
}

func handleRemoveListItems(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	if list.UserID != uid {
		return response.Forbidden(c, "you do not have permission to modify this list")
	}

	var req removeItemsRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}

	removed := 0

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := advisoryListLock(tx, listID); err != nil {
			return nil, err
		}

		// Remove by item IDs
		if len(req.IDs) > 0 {
			res := tx.Where("list_id = ? AND id IN ?", listID, req.IDs).Delete(&database.ListItem{})
			removed += int(res.RowsAffected)
		}

		// Remove by media references
		removeByMedia := func(items []mediaRef, mediaType string) {
			for _, ref := range items {
				mediaID, ok := resolveMediaRef(tx, mediaType, ref)
				if !ok {
					continue
				}
				res := tx.Where("list_id = ? AND media_type = ? AND media_id = ?", listID, mediaType, mediaID).Delete(&database.ListItem{})
				removed += int(res.RowsAffected)
			}
		}
		removeByMedia(req.Movies, "movie")
		removeByMedia(req.Shows, "show")

		if removed > 0 {
			bumpListVersion(cacheTx, listID)
			if err := contractPublish(tx, outbox.EventListItemsRemoved, uid, map[string]any{
				"list_id": listID,
				"removed": removed,
			}); err != nil {
				return nil, err
			}
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.JSON(removeItemsResponse{Removed: removed})
}

func handleReorderListItems(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	if list.UserID != uid {
		return response.Forbidden(c, "you do not have permission to modify this list")
	}

	var req reorderRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}

	if len(req.Order) == 0 {
		return response.BadRequest(c, "order array must not be empty")
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		if err := advisoryListLock(tx, listID); err != nil {
			return nil, err
		}

		// Assign rank based on position in the order array
		for i, itemID := range req.Order {
			if err := tx.Model(&database.ListItem{}).Where("id = ? AND list_id = ?", itemID, listID).Update("rank", i).Error; err != nil {
				return nil, err
			}
		}

		// Mark as ranked
		if err := tx.Model(&list).Update("is_ranked", true).Error; err != nil {
			return nil, err
		}

		bumpListVersion(cacheTx, listID)
		bumpUserVersion(cacheTx, uid)
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.JSON(fiber.Map{"status": "reordered", "count": len(req.Order)})
}

// ============================================================================
// LIKES
// ============================================================================

type likeUserResponse struct {
	UserID    string `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

func handleGetListLikes(c *fiber.Ctx) error {
	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	var likes []database.ListLike
	if err := db.Where("list_id = ?", listID).Order("created_at DESC").Find(&likes).Error; err != nil {
		return response.InternalError(c)
	}
	if likes == nil {
		likes = make([]database.ListLike, 0)
	}

	results := make([]likeUserResponse, 0, len(likes))
	for _, l := range likes {
		results = append(results, likeUserResponse{
			UserID:    l.UserID,
			CreatedAt: response.FormatTime(l.CreatedAt),
		})
	}

	return c.JSON(results)
}

func handleLikeList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		// Insert like (ignore duplicates)
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.ListLike{
			ListID: listID,
			UserID: uid,
		})

		if res.RowsAffected > 0 {
			if err := tx.Model(&list).UpdateColumn("like_count", gorm.Expr("like_count + 1")).Error; err != nil {
				return nil, err
			}
			bumpListVersion(cacheTx, listID)
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.JSON(fiber.Map{"liked": true})
}

// ============================================================================
// COMMENTS
// ============================================================================

func handleGetListComments(c *fiber.Ctx) error {
	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	var list database.List
	if err := db.First(&list, listID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, "list not found")
		}
		return response.InternalError(c)
	}

	page, limit := response.ParsePagination(c, 25)

	var total int64
	if err := db.Model(&database.ListComment{}).Where("list_id = ?", listID).Count(&total).Error; err != nil {
		return response.InternalError(c)
	}

	var comments []database.ListComment
	if err := db.Where("list_id = ?", listID).
		Order("created_at ASC, id ASC"). // stable order for pagination
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&comments).Error; err != nil {
		return response.InternalError(c)
	}
	if comments == nil {
		comments = make([]database.ListComment, 0)
	}

	response.SetPaginationHeaders(c, page, limit, total, len(comments))

	return c.JSON(comments)
}

// ============================================================================
// POPULAR / TRENDING
// ============================================================================

func handleGetPopularLists(c *fiber.Ctx) error {
	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	period := c.Query("period", "all")
	page, limit := response.ParsePagination(c, 25)

	query := db.Where("privacy = ?", "public")

	switch period {
	case "weekly":
		since := time.Now().AddDate(0, 0, -7)
		query = query.Where("created_at >= ?", since)
	case "monthly":
		since := time.Now().AddDate(0, -1, 0)
		query = query.Where("created_at >= ?", since)
	}

	var total int64
	query.Model(&database.List{}).Count(&total)

	var lists []database.List
	if err := query.Order("like_count DESC, created_at DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&lists).Error; err != nil {
		return response.InternalError(c)
	}
	if lists == nil {
		lists = make([]database.List, 0)
	}

	out, err := contractListObjects(db, lists)
	if err != nil {
		return response.InternalError(c)
	}

	return c.JSON(fiber.Map{
		"items":     out,
		"page":      page,
		"limit":     limit,
		"row_count": total,
	})
}

func handleGetTrendingLists(c *fiber.Ctx) error {
	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}

	page, limit := response.ParsePagination(c, 25)

	// Trending = recently created public lists with most likes (momentum).
	// Simplified: sorts by recent created_at with some like weight.
	var total int64
	db.Model(&database.List{}).Where("privacy = ?", "public").Count(&total)

	var lists []database.List
	if err := db.Where("privacy = ?", "public").
		Order("(like_count * 10 + EXTRACT(EPOCH FROM created_at) / 86400)::bigint DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&lists).Error; err != nil {
		return response.InternalError(c)
	}
	if lists == nil {
		lists = make([]database.List, 0)
	}

	out, err := contractListObjects(db, lists)
	if err != nil {
		return response.InternalError(c)
	}

	return c.JSON(fiber.Map{
		"items":     out,
		"page":      page,
		"limit":     limit,
		"row_count": total,
	})
}

// ============================================================================
