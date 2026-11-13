package sync

import (
	"github.com/haaku-tv/haaku/internal/contractshape"
	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// Item shapes for the sync collections.
//
// One row of a collection is a Trakt ITEM: the join row's own envelope
// (id/rank/listed_at/rating/…) plus the media object it points at, nested under
// the media type's key:
//
//	{"rank": 1, "id": 42, "listed_at": "…", "type": "movie", "movie": {…}}
//
// Trakt clients (and this repo's own web pages) read the nested object — the
// title, the images, and the `ids` block a follow-up write sends back. Serving
// only `media_type`/`media_id` hands a client a row it can neither render nor
// dereference, which is why every builder here takes the db and hydrates.
//
// `id` is the JOIN ROW id (the value a reorder or a remove accepts), never the
// media id, which lives only inside the nested object's `ids`.

// watchlistItem renders one watchlist row with its media object.
func watchlistItem(w *database.UserWatchlist, media map[contractshape.MediaRef]map[string]any) map[string]any {
	row := map[string]any{
		"id":         w.ID,
		"rank":       w.Rank,
		"listed_at":  response.FormatTime(w.ListedAt),
		"type":       w.MediaType,
		"media_type": w.MediaType,
		"media_id":   w.MediaID,
	}
	if w.Notes != nil {
		row["notes"] = *w.Notes
	}
	attachMedia(row, w.MediaType, w.MediaID, media)
	return row
}

// favoriteItem renders one favorites row with its media object. Trakt names the
// timestamp `listed_at`; `favorited_at` is kept for clients already reading it.
func favoriteItem(f *database.UserFavorite, media map[contractshape.MediaRef]map[string]any) map[string]any {
	row := map[string]any{
		"id":           f.ID,
		"rank":         f.Rank,
		"listed_at":    response.FormatTime(f.FavoritedAt),
		"favorited_at": response.FormatTime(f.FavoritedAt),
		"type":         f.MediaType,
		"media_type":   f.MediaType,
		"media_id":     f.MediaID,
	}
	attachMedia(row, f.MediaType, f.MediaID, media)
	return row
}

// ratingItem renders one rating row with its media object.
func ratingItem(r *database.UserRating, media map[contractshape.MediaRef]map[string]any) map[string]any {
	row := map[string]any{
		"id":         r.ID,
		"rated_at":   response.FormatTime(r.RatedAt),
		"rating":     r.Rating,
		"type":       r.MediaType,
		"media_type": r.MediaType,
		"media_id":   r.MediaID,
	}
	attachMedia(row, r.MediaType, r.MediaID, media)
	return row
}

// collectionItem renders one collection row with its media object.
func collectionItem(c *database.UserCollection, media map[contractshape.MediaRef]map[string]any) map[string]any {
	row := map[string]any{
		"id":           c.ID,
		"collected_at": response.FormatTime(c.CollectedAt),
		"type":         c.MediaType,
		"media_type":   c.MediaType,
		"media_id":     c.MediaID,
	}
	attachMedia(row, c.MediaType, c.MediaID, media)
	return row
}

// historyItem renders one history row with its media object. An episode history
// row carries BOTH the episode and the owning show: the contract's history
// entry holds both, and the web history page titles a row from the show.
func historyItem(h *database.UserHistory, media map[contractshape.MediaRef]map[string]any, episodeShows map[int64]int64) map[string]any {
	mediaType, mediaID := h.MediaType, h.MediaID
	if mediaType == "episode" && h.EpisodeID != nil {
		mediaID = *h.EpisodeID
	}
	row := map[string]any{
		"id":         h.ID,
		"watched_at": response.FormatTime(h.WatchedAt),
		"action":     historyAction(h.Source),
		"type":       mediaType,
		"media_type": h.MediaType,
		"media_id":   h.MediaID,
	}
	if h.Source != "" {
		row["source"] = h.Source
	}
	if h.SeasonID != nil {
		row["season_id"] = *h.SeasonID
	}
	if h.EpisodeID != nil {
		row["episode_id"] = *h.EpisodeID
	}
	attachMedia(row, mediaType, mediaID, media)
	if mediaType == "episode" {
		if showID, ok := episodeShows[mediaID]; ok {
			attachMedia(row, "show", showID, media)
		}
	}
	return row
}

// attachMedia nests the media object under its type's key. A dangling reference
// is left out; the row still carries media_type/media_id, so a client can tell
// the item exists while its media row is gone.
func attachMedia(row map[string]any, mediaType string, mediaID int64, media map[contractshape.MediaRef]map[string]any) {
	if obj, ok := media[contractshape.MediaRef{Type: mediaType, ID: mediaID}]; ok {
		row[mediaType] = obj
	}
}

func watchlistRefs(rows []database.UserWatchlist) []contractshape.MediaRef {
	refs := make([]contractshape.MediaRef, 0, len(rows))
	for i := range rows {
		refs = append(refs, contractshape.MediaRef{Type: rows[i].MediaType, ID: rows[i].MediaID})
	}
	return refs
}

func favoriteRefs(rows []database.UserFavorite) []contractshape.MediaRef {
	refs := make([]contractshape.MediaRef, 0, len(rows))
	for i := range rows {
		refs = append(refs, contractshape.MediaRef{Type: rows[i].MediaType, ID: rows[i].MediaID})
	}
	return refs
}

func ratingRefs(rows []database.UserRating) []contractshape.MediaRef {
	refs := make([]contractshape.MediaRef, 0, len(rows))
	for i := range rows {
		refs = append(refs, contractshape.MediaRef{Type: rows[i].MediaType, ID: rows[i].MediaID})
	}
	return refs
}

func collectionRefs(rows []database.UserCollection) []contractshape.MediaRef {
	refs := make([]contractshape.MediaRef, 0, len(rows))
	for i := range rows {
		refs = append(refs, contractshape.MediaRef{Type: rows[i].MediaType, ID: rows[i].MediaID})
	}
	return refs
}

// historyRefs collects the refs a history page needs: the row's own media row,
// and (for an episode row) the show resolved later from the episode's parent.
func historyRefs(rows []database.UserHistory) []contractshape.MediaRef {
	refs := make([]contractshape.MediaRef, 0, len(rows))
	for i := range rows {
		if rows[i].MediaType == "episode" {
			id := rows[i].MediaID
			if rows[i].EpisodeID != nil {
				id = *rows[i].EpisodeID
			}
			refs = append(refs, contractshape.MediaRef{Type: "episode", ID: id})
			continue
		}
		refs = append(refs, contractshape.MediaRef{Type: rows[i].MediaType, ID: rows[i].MediaID})
	}
	return refs
}

// buildWatchlistItems hydrates a page of watchlist rows.
func buildWatchlistItems(db *gorm.DB, rows []database.UserWatchlist) ([]map[string]any, error) {
	media, err := contractshape.MediaObjects(db, watchlistRefs(rows))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, watchlistItem(&rows[i], media))
	}
	return out, nil
}

// buildFavoriteItems hydrates a page of favorites rows.
func buildFavoriteItems(db *gorm.DB, rows []database.UserFavorite) ([]map[string]any, error) {
	media, err := contractshape.MediaObjects(db, favoriteRefs(rows))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, favoriteItem(&rows[i], media))
	}
	return out, nil
}

// buildRatingItems hydrates a page of rating rows.
func buildRatingItems(db *gorm.DB, rows []database.UserRating) ([]map[string]any, error) {
	media, err := contractshape.MediaObjects(db, ratingRefs(rows))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, ratingItem(&rows[i], media))
	}
	return out, nil
}

// buildCollectionItems hydrates a page of collection rows.
func buildCollectionItems(db *gorm.DB, rows []database.UserCollection) ([]map[string]any, error) {
	media, err := contractshape.MediaObjects(db, collectionRefs(rows))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, collectionItem(&rows[i], media))
	}
	return out, nil
}

// buildHistoryItems hydrates a page of history rows. Episode rows need one
// extra lookup — an episode object does not carry its show — so the parents are
// resolved for the whole page at once rather than per row.
func buildHistoryItems(db *gorm.DB, rows []database.UserHistory) ([]map[string]any, error) {
	media, episodeShows, err := historyMedia(db, rows)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, historyItem(&rows[i], media, episodeShows))
	}
	return out, nil
}

// buildHistoryItem hydrates one history row.
func buildHistoryItem(db *gorm.DB, h *database.UserHistory) (map[string]any, error) {
	media, episodeShows, err := historyMedia(db, []database.UserHistory{*h})
	if err != nil {
		return nil, err
	}
	return historyItem(h, media, episodeShows), nil
}

// writeItems writes a collection page from a builder's two results, turning a
// failed hydration into a 500 rather than a page with its media stripped.
func writeItems[T any](c *fiber.Ctx, items []T, err error, page, limit int, total int64) error {
	if err != nil {
		return response.InternalError(c)
	}
	return writeCollectionPage(c, items, page, limit, total, len(items))
}

// writeCollectionPage writes one collection page: the {items,page,limit,row_count}
// envelope these internal routes serve, plus the X-Pagination-* headers.
//
// The envelope is ours — the contract-shaped routes under /sync/{collection}/{type}/…
// serve Trakt's bare array — so a client that reads headers instead of the
// envelope must still get the same numbers, and both surfaces report one total.
func writeCollectionPage(c *fiber.Ctx, items any, page, limit int, total int64, count int) error {
	response.SetPaginationHeaders(c, page, limit, total, count)
	return c.JSON(fiber.Map{
		"items":     items,
		"page":      page,
		"limit":     limit,
		"row_count": total,
	})
}

// historyMedia hydrates a history page: the rows' own media, plus (for episode
// rows) the parent show the contract's history entry carries.
func historyMedia(db *gorm.DB, rows []database.UserHistory) (map[contractshape.MediaRef]map[string]any, map[int64]int64, error) {
	media, err := contractshape.MediaObjects(db, historyRefs(rows))
	if err != nil {
		return nil, nil, err
	}
	episodeShows, shows, err := historyEpisodeShows(db, rows)
	if err != nil {
		return nil, nil, err
	}
	if len(shows) > 0 {
		showObjects, err := contractshape.MediaObjects(db, shows)
		if err != nil {
			return nil, nil, err
		}
		for ref, obj := range showObjects {
			media[ref] = obj
		}
	}
	return media, episodeShows, nil
}

// historyEpisodeShows maps each episode row's id to its parent show id and
// returns the show refs that still need hydrating.
func historyEpisodeShows(db *gorm.DB, rows []database.UserHistory) (map[int64]int64, []contractshape.MediaRef, error) {
	var episodeIDs []int64
	for i := range rows {
		if rows[i].MediaType != "episode" {
			continue
		}
		id := rows[i].MediaID
		if rows[i].EpisodeID != nil {
			id = *rows[i].EpisodeID
		}
		episodeIDs = append(episodeIDs, id)
	}
	episodeShows, err := contractshape.EpisodeShowIDs(db, episodeIDs)
	if err != nil {
		return nil, nil, err
	}
	shows := make([]contractshape.MediaRef, 0, len(episodeShows))
	for _, showID := range episodeShows {
		shows = append(shows, contractshape.MediaRef{Type: "show", ID: showID})
	}
	return episodeShows, shows, nil
}
