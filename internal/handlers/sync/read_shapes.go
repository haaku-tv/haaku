package sync

import (
	"github.com/haaku-tv/haaku/internal/database"

	"gorm.io/gorm"
)

// Exported views of the item shapes and their builders. The public-profile read
// surface (/v1/users/{id}/history, /collection, /watchlist, /ratings) returns
// exactly what the caller's own /v1/sync/... routes return, so both surfaces
// share one definition instead of drifting apart.
//
// Every builder takes the db because an item is a row envelope PLUS the media
// object it points at, hydrated in one batched lookup per page.

// HistoryResponse is a single watch-history item.
type HistoryResponse = map[string]any

// CollectionResponse is a single collection item.
type CollectionResponse = map[string]any

// WatchlistResponse is a single watchlist item.
type WatchlistResponse = map[string]any

// RatingResponse is a single rating item.
type RatingResponse = map[string]any

// FavoriteResponse is a single favorites item.
type FavoriteResponse = map[string]any

// HistoryAction maps a stored history row's source onto Trakt's action
// vocabulary (scrobble|checkin|watch). Exported because the public-profile
// contract reads render the same rows at /users/{id}/history/*.
func HistoryAction(source string) string { return historyAction(source) }

// BuildHistoryResponse renders one history row with its media object.
func BuildHistoryResponse(db *gorm.DB, h *database.UserHistory) (HistoryResponse, error) {
	return buildHistoryItem(db, h)
}

// BuildHistoryResponses renders a whole history page with ONE hydration pass.
// Callers page through rows, so a per-row builder would be one lookup chain per
// row; this is the shape every list handler should use.
func BuildHistoryResponses(db *gorm.DB, items []database.UserHistory) ([]HistoryResponse, error) {
	return buildHistoryItems(db, items)
}

// BuildCollectionResponses renders collection rows with their media objects.
func BuildCollectionResponses(db *gorm.DB, items []database.UserCollection) ([]CollectionResponse, error) {
	return buildCollectionItems(db, items)
}

// BuildWatchlistResponses renders watchlist rows with their media objects.
func BuildWatchlistResponses(db *gorm.DB, items []database.UserWatchlist) ([]WatchlistResponse, error) {
	return buildWatchlistItems(db, items)
}

// BuildRatingResponses renders rating rows with their media objects.
func BuildRatingResponses(db *gorm.DB, items []database.UserRating) ([]RatingResponse, error) {
	return buildRatingItems(db, items)
}

// BuildFavoriteResponses renders favorites rows with their media objects.
func BuildFavoriteResponses(db *gorm.DB, items []database.UserFavorite) ([]FavoriteResponse, error) {
	return buildFavoriteItems(db, items)
}
