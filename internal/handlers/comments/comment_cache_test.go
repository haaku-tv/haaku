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

// A reply is authored by someone other than the parent's author, so the write
// has to invalidate BOTH accounts' stats: the replier's comment count changed,
// and the parent's cached response embeds the reply/comment counters too.
func TestBumpCommentWriteInvalidatesBothAuthors(t *testing.T) {
	mediaID := int64(7)
	parent := &database.Comment{ID: 1, UserID: "parent-author", MediaType: "movie", MediaID: &mediaID}
	reply := &database.Comment{ID: 2, UserID: "replier", ParentID: &parent.ID, MediaType: "movie", MediaID: &mediaID}

	tx := cache.NewCacheTransaction()
	bumpCommentWrite(tx, parent, reply)

	keys := tx.QueuedInvalidationKeys()
	for _, want := range []string{redis.BuildUserVersionKey("parent-author"), redis.BuildUserVersionKey("replier")} {
		found := false
		for _, key := range keys {
			if key == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("bumpCommentWrite queued %v; %q is missing, so that account's cached stats cannot see the reply", keys, want)
		}
	}
}
