package media

import (
	"strconv"

	"github.com/haaku-tv/haaku/internal/database"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// Route-typed resolution for the media types a route can name beyond the three
// the type-agnostic resolver knows.
//
// ResolveID and ResolveIDAs answer by WRITING the 404 envelope, which is the
// right answer for /movies/{id} ("not a movie"). A history item route has its
// own vocabulary for that case — 400 "media_type does not match the resolved
// media id", the answer it has given since before #93 — and it must also be
// able to address an EPISODE or a SEASON: user_history stores those by their
// LOCAL row id (episodes.id / seasons.id), and the type-agnostic resolver never
// looks in either table, so those rows were unreachable (issue #96).
//
// So this is the same resolution with the writing left to the caller: it says
// what the id IS, and the handler chooses the wording. The route's type stays
// authoritative, exactly as ResolveIDAs documents — an id that names a row of
// the type the route names resolves even when another table carries the same
// number.

// Failure says why a route id did not resolve to the media type the route
// named. It is not an error: the resolver answers what the id is, which is not
// a failure of the lookup.
type Failure int

const (
	// FailureNone: the id names a row of the wanted type.
	FailureNone Failure = iota
	// FailureMissing: no media row of any type carries the id.
	FailureMissing
	// FailureWrongType: the id names a row of a DIFFERENT media type.
	FailureWrongType
	// FailureBadIDType: ?id_type= is not one of tmdb, imdb, tvdb.
	FailureBadIDType
)

// routeMediaModels is every media type a route may name, with the table its
// local row id lives in. It is deliberately wider than mediaTypeModels:
// seasons and episodes are addressable by route — their ids are what
// user_history stores — but they are not media the type-agnostic resolver may
// return, so adding them there would change what /movies/{id} answers for an
// id that happens to be a season.
var routeMediaModels = map[string]func() any{
	"movie":   func() any { return &database.Movie{} },
	"show":    func() any { return &database.Show{} },
	"person":  func() any { return &database.Person{} },
	"season":  func() any { return &database.Season{} },
	"episode": func() any { return &database.Episode{} },
}

// routeMediaOrder is the precedence the wrong-type probe walks. It is fixed so
// that a number several tables happen to carry is reported the same way every
// time; the wanted type has already been ruled out when it runs.
var routeMediaOrder = []string{"movie", "show", "person", "season", "episode"}

// ResolveRouteID resolves a route's id against the media type the route names,
// without writing anything. want is one of movie, show, person, season,
// episode.
//
// The numeric form reads the local row id in want's table first, so an episode
// id is reachable at /sync/history/episodes/{id} even when movie {id} exists.
// The slug and ?id_type= forms address a movie or a show only: seasons and
// episodes have no slug column and no external_ids rows, so a route naming either
// one answers FailureMissing for them rather than resolving a different type's
// row.
func ResolveRouteID(c *fiber.Ctx, param, want string) (int64, Failure, error) {
	if _, ok := routeMediaModels[want]; !ok {
		return 0, FailureNone, fiber.NewError(fiber.StatusInternalServerError, "ResolveRouteID requires a media type")
	}

	if idType := c.Query("id_type"); idType != "" {
		switch idType {
		case "tmdb", "imdb", "tvdb":
		default:
			return 0, FailureBadIDType, nil
		}
		if want != "movie" && want != "show" {
			return 0, FailureMissing, nil
		}
		return classifyRouteExternal(c, param, idType, want)
	}
	if !isNumeric(param) {
		if want != "movie" && want != "show" {
			return 0, FailureMissing, nil
		}
		return classifyRouteExternal(c, param, "slug", want)
	}

	id, err := strconv.ParseInt(param, 10, 64)
	if err != nil {
		// isNumeric already ruled out syntax, so the only failure left is an id
		// too large for the column: no row can carry it, which is a miss rather
		// than a query failure.
		return 0, FailureMissing, nil //nolint:nilerr // an out-of-range id is a miss, not a failure
	}
	db, dbErr := database.GetDB()
	if dbErr != nil {
		return 0, FailureNone, fiber.NewError(fiber.StatusInternalServerError, "database unavailable")
	}
	found, err := routeRowExists(db, want, id)
	if err != nil {
		return 0, FailureNone, err
	}
	if found {
		return id, FailureNone, nil
	}
	// Not the wanted type: find out whether it is missing outright or simply
	// another media type, so the caller can keep the two answers apart.
	for _, other := range routeMediaOrder {
		if other == want {
			continue
		}
		found, err := routeRowExists(db, other, id)
		if err != nil {
			return 0, FailureNone, err
		}
		if found {
			return 0, FailureWrongType, nil
		}
	}
	return 0, FailureMissing, nil
}

// classifyRouteExternal resolves an external id or slug against want, and says
// whether the identifier belongs to the other type the sources can carry.
func classifyRouteExternal(c *fiber.Ctx, externalID, source, want string) (int64, Failure, error) {
	db, dbErr := database.GetDB()
	if dbErr != nil {
		return 0, FailureNone, fiber.NewError(fiber.StatusInternalServerError, "database unavailable")
	}
	id, found, err := lookupExternal(db, source, externalID, want)
	if err != nil {
		return 0, FailureNone, fiber.NewError(fiber.StatusInternalServerError, "database query error")
	}
	if found {
		return id, FailureNone, nil
	}
	for _, other := range []string{"movie", "show"} {
		if other == want {
			continue
		}
		if _, ok, otherErr := lookupExternal(db, source, externalID, other); otherErr != nil {
			return 0, FailureNone, fiber.NewError(fiber.StatusInternalServerError, "database query error")
		} else if ok {
			return 0, FailureWrongType, nil
		}
	}
	return 0, FailureMissing, nil
}

// routeRowExists reports whether that media type's table holds this id.
func routeRowExists(db *gorm.DB, mediaType string, id int64) (bool, error) {
	model, ok := routeMediaModels[mediaType]
	if !ok {
		return false, fiber.NewError(fiber.StatusInternalServerError, "unknown media type: "+mediaType)
	}
	var count int64
	if err := db.Model(model()).Where("id = ?", id).Count(&count).Error; err != nil {
		return false, fiber.NewError(fiber.StatusInternalServerError, "database query error")
	}
	return count > 0, nil
}
