package comments

import (
	"github.com/haaku-tv/haaku/internal/cache"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/redis"
)

// bumpCommentTarget queues version-counter bumps for the media/list a comment
// is attached to, so cached media/list responses (which embed comments, likes
// and reactions) are invalidated in O(1) after any comment write, and for the
// author, whose comment count the user stats payload reports — the cached stats
// response is keyed by the user version and no other write path advances it
// when a comment is created or deleted. The webhook catalog defines no
// comment/reaction events, so no outbox event is emitted.
func bumpCommentTarget(cacheTx *cache.CacheTransaction, comment *database.Comment) {
	if comment == nil {
		return
	}
	if comment.UserID != "" {
		uvk := redis.BuildUserVersionKey(comment.UserID)
		cacheTx.AddInvalidation(uvk, map[string]interface{}{
			"user_id":      comment.UserID,
			"version_key":  uvk,
			"version_only": true,
		})
	}
	if comment.MediaType != "" && comment.MediaID != nil && *comment.MediaID != 0 {
		mvk := redis.BuildMediaVersionKey(comment.MediaType, *comment.MediaID, nil, nil)
		cacheTx.AddInvalidation(mvk, map[string]interface{}{
			"media_id":     *comment.MediaID,
			"media_type":   comment.MediaType,
			"version_key":  mvk,
			"version_only": true,
		})
	}
	if comment.ListID != nil && *comment.ListID != 0 {
		lvk := redis.BuildListVersionKey(*comment.ListID)
		cacheTx.AddInvalidation(lvk, map[string]interface{}{
			"list_id":      *comment.ListID,
			"version_key":  lvk,
			"version_only": true,
		})
	}
}
