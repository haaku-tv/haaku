package lists

// The unlike handler lives in its own file because lists.go is over the size
// ratchet's soft limit and may only shrink (scripts/check-size.sh); the
// contract fix in it had to land somewhere that does not grow that file.

import (
	"strconv"

	"github.com/haaku-tv/haaku/internal/cache"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// handleUnlikeList deletes the caller's like on a list.
//
// The contract declares 204 and no body for DELETE /lists/{id}/like, so this
// answers exactly that: a client switching on the status code sees the
// declared contract, and the empty body is the one the contract defines. The
// delete itself is real — the list_likes row is removed and like_count is
// decremented in the same transaction (an unlike of a like that is not there
// is a no-op, which is what an idempotent DELETE means).
func handleUnlikeList(c *fiber.Ctx) error {
	uid := userID(c)

	idParam := c.Params("id")
	listID, err := strconv.ParseInt(idParam, 10, 64)
	if err != nil {
		return response.BadRequest(c, "invalid list id")
	}

	err = cache.WithCacheTransaction(func(tx *gorm.DB) (*cache.CacheTransaction, error) {
		cacheTx := cache.NewCacheTransaction()

		res := tx.Where("list_id = ? AND user_id = ?", listID, uid).Delete(&database.ListLike{})
		// A failed DELETE reports RowsAffected 0, which would otherwise be read
		// as "there was no like" and answered 204 for a delete that never
		// happened. Return the driver's error; the wrapper turns it into a 500.
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected > 0 {
			// CASE rather than GREATEST: GREATEST is Postgres-only, so the
			// sqlite handler suite could never execute this branch and the
			// delete itself went untested. CASE clamps at zero identically on
			// both databases.
			if err := tx.Model(&database.List{}).Where("id = ?", listID).
				UpdateColumn("like_count", gorm.Expr("CASE WHEN like_count > 0 THEN like_count - 1 ELSE 0 END")).Error; err != nil {
				return nil, err
			}
			bumpListVersion(cacheTx, listID)
		}
		return cacheTx, nil
	})
	if err != nil {
		return response.InternalError(c)
	}

	return c.SendStatus(fiber.StatusNoContent)
}
