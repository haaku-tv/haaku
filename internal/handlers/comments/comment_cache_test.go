package comments

import (
	"testing"

	"github.com/haaku-tv/haaku/internal/cache"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/redis"
)

// TestBumpCommentTargetInvalidatesTheAuthorLocks the write-side half of the
// user stats payload (issue #98): the comment count is an aggregate over the
// author's rows, and the cached stats response is keyed by the author's user
// version — so creating or deleting a comment has to advance that version or
// the profile shows a stale count until some unrelated write moves it.
func TestBumpCommentTargetInvalidatesTheAuthor(t *testing.T) {
	mediaID := int64(7)
	comment := &database.Comment{
		ID:        1,
		UserID:    "author-1",
		MediaType: "movie",
		MediaID:   &mediaID,
	}

	tx := cache.NewCacheTransaction()
	bumpCommentTarget(tx, comment)

	want := redis.BuildUserVersionKey("author-1")
	for _, key := range tx.QueuedInvalidationKeys() {
		if key == want {
			return
		}
	}
	t.Fatalf("bumpCommentTarget queued %v; the author's user version key %q is missing, so the cached stats payload cannot see a new comment",
		tx.QueuedInvalidationKeys(), want)
}

// A nil comment (an id that no longer resolves) must not panic and must not
// queue anything.
func TestBumpCommentTargetIgnoresNil(t *testing.T) {
	tx := cache.NewCacheTransaction()
	bumpCommentTarget(tx, nil)
	if keys := tx.QueuedInvalidationKeys(); len(keys) != 0 {
		t.Fatalf("bumpCommentTarget(nil) queued %v, want nothing", keys)
	}
}
