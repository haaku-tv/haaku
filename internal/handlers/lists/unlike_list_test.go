package lists

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
)

// TestUnlikeListAnswers204AndDeletesTheLike is the regression test for the
// DELETE /lists/{id}/like row (#141).
//
// The row was graded `implemented` while the handler answered 200 with
// `{"liked":false}`; the contract declares 204 and no body. Both halves of the
// acceptance are asserted here, because the status alone proves nothing: a 204
// that silently did nothing would be worse than the wrong code. So the test
// seeds a like of the caller's, deletes it, and reads the store back: the row
// is gone, the like_count is decremented, another user's like on the same list
// survives, and a second DELETE stays a 204 no-op.
func TestUnlikeListAnswers204AndDeletesTheLike(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractUser(t, db, "stranger")

	now := time.Now().UTC().Truncate(time.Second)
	list := database.List{UserID: "owner", Name: "Liked", Slug: "liked", Privacy: "public",
		CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&list).Error; err != nil {
		t.Fatalf("seed list: %v", err)
	}
	if err := db.Create(&database.ListLike{ListID: list.ID, UserID: "owner", CreatedAt: now}).Error; err != nil {
		t.Fatalf("seed caller like: %v", err)
	}
	if err := db.Create(&database.ListLike{ListID: list.ID, UserID: "stranger", CreatedAt: now}).Error; err != nil {
		t.Fatalf("seed other like: %v", err)
	}
	if err := db.Model(&database.List{}).Where("id = ?", list.ID).
		UpdateColumn("like_count", 2).Error; err != nil {
		t.Fatalf("seed like_count: %v", err)
	}

	// The legacy mount is the one that answers the contract path
	// DELETE /v1/lists/{id}/like (the /users/{id}/lists/{list_id}/like mount is
	// a different handler, already 204). Auth is pinned by X-Test-User, the
	// same shim the rest of this package's contract tests use.
	app := fiber.New()
	v1 := app.Group("/v1")
	v1.Use(func(c *fiber.Ctx) error {
		if u := c.Get("X-Test-User"); u != "" {
			c.Locals("user_id", u)
		}
		return c.Next()
	})
	v1.Delete("/lists/:id/like", handleUnlikeList)

	path := "/v1/lists/" + strconv.FormatInt(list.ID, 10) + "/like"
	status, raw := contractDo(t, app, "owner", http.MethodDelete, path, nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE %s = %d (%s), want 204", path, status, raw)
	}
	if len(raw) != 0 {
		t.Errorf("DELETE %s body = %q, want empty — the contract's 204 declares no body", path, raw)
	}

	var mine int64
	if err := db.Model(&database.ListLike{}).
		Where("list_id = ? AND user_id = ?", list.ID, "owner").Count(&mine).Error; err != nil {
		t.Fatalf("count caller likes: %v", err)
	}
	if mine != 0 {
		t.Errorf("caller's like row survived the delete (%d rows), the 204 claimed a delete that did not happen", mine)
	}

	var theirs int64
	if err := db.Model(&database.ListLike{}).
		Where("list_id = ? AND user_id = ?", list.ID, "stranger").Count(&theirs).Error; err != nil {
		t.Fatalf("count other likes: %v", err)
	}
	if theirs != 1 {
		t.Errorf("another user's like on the list = %d rows, want 1 kept — the delete must be scoped to the caller", theirs)
	}

	var stored database.List
	if err := db.First(&stored, list.ID).Error; err != nil {
		t.Fatalf("reload list: %v", err)
	}
	if stored.LikeCount != 1 {
		t.Errorf("like_count = %d after the unlike, want 1 (2 - 1)", stored.LikeCount)
	}

	// An unlike of a like that is not there is still a 204 — idempotent DELETE —
	// and must not move the counter again.
	status, raw = contractDo(t, app, "owner", http.MethodDelete, path, nil)
	if status != http.StatusNoContent {
		t.Fatalf("second DELETE %s = %d (%s), want 204", path, status, raw)
	}
	if len(raw) != 0 {
		t.Errorf("second DELETE body = %q, want empty", raw)
	}
	if err := db.First(&stored, list.ID).Error; err != nil {
		t.Fatalf("reload list: %v", err)
	}
	if stored.LikeCount != 1 {
		t.Errorf("like_count = %d after a repeated unlike, want 1 (unchanged)", stored.LikeCount)
	}
}
