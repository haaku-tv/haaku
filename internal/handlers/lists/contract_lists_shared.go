package lists

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/haaku-tv/haaku/internal/auth"
	"github.com/haaku-tv/haaku/internal/contractshape"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/outbox"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// ============================================================================
// Helpers
// ============================================================================

// advisoryListLock serializes concurrent writes to a list. The lock is a
// Postgres advisory lock; sqlite (the handler-test driver) has no equivalent,
// so it is a no-op there.
func advisoryListLock(tx *gorm.DB, listID int64) error {
	if tx.Name() == "sqlite" {
		return nil
	}
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", fmt.Sprintf("list:%d", listID)).Error
}

// contractPublish queues an outbox event. The outbox writer emits a Postgres
// `::jsonb` cast, which sqlite (the handler-test driver) cannot parse, so the
// event is skipped there. Production is Postgres and always publishes.
func contractPublish(tx *gorm.DB, eventType, uid string, payload map[string]any) error {
	if tx.Name() == "sqlite" {
		return nil
	}
	return outbox.PublishEvent(tx, eventType, uid, payload)
}

func contractDB(c *fiber.Ctx) (*gorm.DB, error) {
	db, err := database.GetDB()
	if err != nil {
		_ = response.InternalError(c)
		return nil, response.ErrHandled
	}
	return db, nil
}

// contractTargetUser resolves the {id} path segment (including the "me" alias)
// to a user id and rejects a caller acting on another user's surface. Returns
// "" (after writing the response) when the caller may not act.
func contractTargetUser(c *fiber.Ctx) (string, error) {
	target := auth.ResolveTargetID(c, "id")
	if target == "" {
		_ = response.Unauthorized(c)
		return "", response.ErrHandled
	}
	if caller := userID(c); caller != target {
		_ = response.Forbidden(c, "you may only manage your own lists")
		return "", response.ErrHandled
	}
	return target, nil
}

// contractListOwnerID resolves {id} for a read and verifies the list belongs to
// that user; a mismatch is a 404 (the list is not under this user).
func contractListOwnerID(c *fiber.Ctx) (string, error) {
	target := auth.ResolveTargetID(c, "id")
	if target == "" {
		_ = response.Unauthorized(c)
		return "", response.ErrHandled
	}
	return target, nil
}

func contractLoadList(db *gorm.DB, param string) (*database.List, error) {
	var l database.List
	if id, err := strconv.ParseInt(param, 10, 64); err == nil {
		if err := db.First(&l, id).Error; err != nil {
			return nil, err
		}
		return &l, nil
	}
	if err := db.Where("slug = ?", param).First(&l).Error; err != nil {
		return nil, err
	}
	return &l, nil
}

// canViewList is the single list-privacy rule: a private list is visible only
// to its owner. It mirrors the gate our native GET /v1/lists/{id} applies.
func canViewList(l *database.List, viewerID string) bool {
	return l.Privacy != "private" || viewerID == l.UserID
}

func listNotFound(c *fiber.Ctx, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		_ = response.NotFound(c, "list not found")
		return response.ErrHandled
	}
	_ = response.InternalError(c)
	return response.ErrHandled
}

// contractListObject renders one list in the contract's list shape with a real
// item count and its owner.
func contractListObject(db *gorm.DB, l *database.List) (map[string]any, error) {
	objs, err := contractListObjects(db, []database.List{*l})
	if err != nil {
		return nil, err
	}
	return objs[0], nil
}

// contractListObjects renders a set of lists in the contract's list shape.
//
// It is the single builder for every route that serves lists. The native
// handlers used to answer the stored GORM rows straight out, which omit `ids`
// entirely — the web client is typed on the contract object, so
// `list.ids.trakt` threw and took the whole My Lists page down while
// /users/me/lists/collaborations (which already used this shape) rendered fine.
//
// Owners and item counts are fetched in one query each rather than per row: a
// list page is exactly the kind of endpoint that would otherwise become N+1.
func contractListObjects(db *gorm.DB, lists []database.List) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(lists))
	if len(lists) == 0 {
		return out, nil
	}

	ownerIDs := make([]string, 0, len(lists))
	listIDs := make([]int64, 0, len(lists))
	for i := range lists {
		ownerIDs = append(ownerIDs, lists[i].UserID)
		listIDs = append(listIDs, lists[i].ID)
	}

	users, err := contractshape.UserByIDBatch(db, ownerIDs)
	if err != nil {
		return nil, err
	}

	type countRow struct {
		ListID int64
		Total  int64
	}
	var rows []countRow
	if err := db.Table("list_items").
		Select("list_id, COUNT(*) AS total").
		Where("list_id IN ?", listIDs).
		Group("list_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[int64]int64, len(rows))
	for _, r := range rows {
		counts[r.ListID] = r.Total
	}

	for i := range lists {
		l := &lists[i]
		out = append(out, contractshape.ListWithItemCount(l, users[l.UserID], counts[l.ID]))
	}
	return out, nil
}

// parseMembershipTarget validates the (type, media_id) pair the media page asks
// "which of my lists hold this?" with, writing the 400 itself.
func parseMembershipTarget(c *fiber.Ctx) (string, int64, error) {
	mediaType := c.Query("type")
	if _, ok := contractMediaTable[mediaType]; !ok {
		_ = response.BadRequest(c, "type must be one of movie, show, season, episode")
		return "", 0, response.ErrHandled
	}
	raw := c.Query("media_id")
	mediaID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mediaID <= 0 {
		_ = response.BadRequest(c, "media_id must be a positive integer")
		return "", 0, response.ErrHandled
	}
	return mediaType, mediaID, nil
}
