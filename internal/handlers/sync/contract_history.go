package sync

// The stored-row renderer that used to live here (contractHistoryItems) is
// gone: the single-item history read is hydrated by buildHistoryItems like every
// other history read (issue #160), so a row-shaped body had no caller left.
// What remains are the two shorthands the hydrated read and the contract paths
// both use.

// normalizeHistoryType accepts Trakt's plural type segment as well as our
// singular media_type, so one handler serves both /v1/sync/history/movies/1
// and /v1/sync/history/movie/1 without changing what the singular path means.
func normalizeHistoryType(raw string) string {
	switch raw {
	case "movies":
		return "movie"
	case "shows":
		return "show"
	case "episodes":
		return "episode"
	case "seasons":
		return "season"
	default:
		return raw
	}
}

// historyAction maps our row source onto Trakt's action vocabulary.
func historyAction(source string) string {
	switch source {
	case "scrobble":
		return "scrobble"
	case "checkin":
		return "checkin"
	default:
		return "watch"
	}
}
