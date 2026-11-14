package sync

import (
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/handlers/media"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// Single-item history reads: addressing and row selection.
//
// Two routes serve one item's watch history — the caller's own
// /v1/sync/history/:media_type/:media_id and the public profile's
// /v1/users/:id/history/:media_type/:media_id. Both used media.ResolveID, the
// TYPE-AGNOSTIC resolver, on an id the route had already typed: it knows movies,
// shows and people, so an episode history row (media_type 'episode', media_id =
// episodes.id) resolved to nothing, or to a movie/show that happened to share
// the number, and answered 404/400 instead of the rows (issue #96).
//
// The type in the path is authoritative here — it is the one place these routes
// can be authoritative about it — so resolution is typed, and the row scan is
// defined once for both surfaces instead of twice.

// historyMediaTypes are the media types a history item route can address.
var historyMediaTypes = map[string]bool{
	"movie": true, "show": true, "season": true, "episode": true,
}

// handleGetHistoryItem returns history for a specific media item.
//
// The item is addressed by the media type the path names (issue #96) and the
// rows are HYDRATED (issue #160): the read answers a contract history item —
// the row envelope plus the media object it points at, with the `ids` block a
// client echoes back — not the stored row. Until #160 it served
// {id, media_type, media_id, watched_at, source}, which a client can neither
// render nor dereference; the list route had hydrated since #153, and this
// single-item read is the one a client uses to check "have I watched this?".
//
// It lives here rather than in sync.go because the route's addressing and its
// row selection are one concern: ResolveHistoryItemID and HistoryRowsForItem are
// the two halves of it, and both surfaces share them.
func handleGetHistoryItem(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	mt := normalizeHistoryType(c.Params("media_type"))

	mediaID, err := ResolveHistoryItemID(c, mt, c.Params("media_id"))
	if err != nil {
		return err
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return response.InternalError(c)
	}
	items, err := HistoryRowsForItem(db, userID, mt, mediaID)
	if err != nil {
		return response.InternalError(c)
	}
	hydrated, err := buildHistoryItems(db, items)
	if err != nil {
		return response.InternalError(c)
	}

	// The contract types this read as a list and a client pages it, so it carries
	// the pagination convention. The read is one item's WHOLE history — there is
	// nothing to page — so the page is every row the item has.
	limit := len(hydrated)
	if limit == 0 {
		limit = 1
	}
	response.SetPaginationHeaders(c, 1, limit, int64(len(hydrated)), len(hydrated))
	return c.JSON(hydrated)
}

// ResolveHistoryItemID resolves the id a history item route names against the
// media type in its path, and writes the route's answer when it cannot.
//
// A movie or a show resolves through media.ResolveRouteID, which also reads the
// slug/?id_type= sources. An episode or a season has no slug and no external-id
// rows: it is addressed by its LOCAL row id, which is what user_history stores
// in media_id.
//
// The answers are the ones these routes already gave, which is why the resolver
// is the quiet one: an id that belongs to another media type is a 400 naming the
// mismatch (ResolveIDAs would answer its own 404 "not a movie"), an id that
// exists nowhere is 404 "media not found", and an unknown ?id_type= is the
// resolver's 400.
func ResolveHistoryItemID(c *fiber.Ctx, kind, param string) (int64, error) {
	if !historyMediaTypes[kind] {
		_ = response.BadRequest(c, "invalid media type")
		return 0, response.ErrHandled
	}
	id, failure, err := media.ResolveRouteID(c, param, kind)
	if err != nil {
		return 0, err
	}
	switch failure {
	case media.FailureNone:
		return id, nil
	case media.FailureWrongType:
		_ = response.BadRequest(c, "media_type does not match the resolved media id")
	case media.FailureBadIDType:
		_ = response.BadRequest(c, "invalid id_type: must be tmdb, imdb, or tvdb")
	default: // media.FailureMissing
		_ = response.NotFound(c, "media not found")
	}
	return 0, response.ErrHandled
}

// HistoryRowsForItem returns the history rows one item's read covers, newest
// first, for the caller's own read and the public-profile read alike.
//
// user_history stores movie and episode rows only (see per_type_reads.go), so an
// episode is addressed directly while a season or a show — which has no row of
// its own — is read as its episodes. That is the rule the public-profile
// contract path already applies to /users/:id/history/shows/:item_id, and the
// only reading of a season's or a show's history the stored rows can support.
//
// media_id and episode_id are both matched for an episode row: a row written
// before episode_id was populated still carries the episode's local id in
// media_id, and a row written by handleAddCollection's show/season expansion
// carries it in episode_id.
func HistoryRowsForItem(db *gorm.DB, userID, kind string, id int64) ([]database.UserHistory, error) {
	q := db.Where("user_id = ?", userID)
	switch kind {
	case "movie":
		q = q.Where("media_type = ? AND media_id = ?", "movie", id)
	case "episode":
		q = q.Where("media_type = ? AND (media_id = ? OR episode_id = ?)", "episode", id, id)
	case "season", "show":
		episodeIDs := episodeIDsOf(db, kind, id)
		if len(episodeIDs) == 0 {
			return []database.UserHistory{}, nil
		}
		q = q.Where("media_type = ? AND (media_id IN ? OR episode_id IN ?)",
			"episode", episodeIDs, episodeIDs)
	default:
		// Unreachable: ResolveHistoryItemID admits only the four types above.
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid media type")
	}

	var rows []database.UserHistory
	if err := q.Order("watched_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// episodeIDsOf returns the local episode ids a season or a show holds. A person
// has no episodes, so it returns none — a person id is answerable as a
// wrong-type answer by the resolver, not as a history read.
func episodeIDsOf(db *gorm.DB, kind string, id int64) []int64 {
	var ids []int64
	column := ""
	switch kind {
	case "season":
		column = "season_id"
	case "show":
		column = "show_id"
	default:
		return nil
	}
	_ = db.Model(&database.Episode{}).Where(column+" = ?", id).Pluck("id", &ids).Error
	return ids
}
