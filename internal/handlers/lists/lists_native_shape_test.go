package lists

import (
	"net/http"
	"testing"
	"time"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

// nativeListsTestApp mounts the NATIVE list handlers (lists.go) behind the
// header-selected caller used by the contract harness.
//
// These are the handlers the real app answers /v1/users/me/lists,
// /v1/lists/{id} and /v1/lists/{id}/items with — RegisterRoutes wires them
// ahead of the contract surface — so the shape they serve is the one the web
// client actually consumes.
func nativeListsTestApp() *fiber.App {
	// The app-level error handler, not Fiber's default: a helper that has
	// already written an error envelope returns response.ErrHandled, and only
	// this handler treats that sentinel as a no-op.
	app := fiber.New(fiber.Config{ErrorHandler: response.HandleError})
	v1 := app.Group("/v1")
	v1.Use(func(c *fiber.Ctx) error {
		if u := c.Get("X-Test-User"); u != "" {
			c.Locals("user_id", u)
		}
		return c.Next()
	})
	v1.Get("/users/:id/lists", handleGetUserLists)
	v1.Get("/users/me/lists/containing", handleGetListsContaining)
	v1.Get("/lists/:id", handleGetList)
	v1.Get("/lists/:id/items", handleGetListItems)
	v1.Post("/lists/:id/items", handleAddListItems)
	v1.Post("/lists/:id/items/remove", handleRemoveListItems)
	return app
}

func seedNativeList(t *testing.T, db *gorm.DB, owner, slug string) *database.List {
	t.Helper()
	list := database.List{
		UserID: owner, Name: slug, Slug: slug, Privacy: "public",
		SortBy: "rank", SortHow: "asc", AllowComments: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(&list).Error; err != nil {
		t.Fatalf("seed list %s: %v", slug, err)
	}
	return &list
}

func seedNativeItem(t *testing.T, db *gorm.DB, listID, mediaID int64, rank int) *database.ListItem {
	t.Helper()
	item := database.ListItem{
		ListID: listID, MediaType: "movie", MediaID: mediaID,
		Rank: rank, AddedBy: "owner", AddedAt: time.Now(),
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("seed list item: %v", err)
	}
	return &item
}

// TestUserListsServeContractShape is the regression for the My Lists crash:
// the collection answered stored rows with no `ids`, so `list.ids.trakt` threw
// and the error boundary replaced the page.
func TestUserListsServeContractShape(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractMedia(t, db)
	list := seedNativeList(t, db, "owner", "first-list")
	seedNativeItem(t, db, list.ID, 11, 0)

	status, raw := contractDo(t, nativeListsTestApp(), "owner", http.MethodGet, "/v1/users/me/lists", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/users/me/lists: want 200, got %d (%s)", status, raw)
	}
	rows := decodeArray(t, raw)
	if len(rows) != 1 {
		t.Fatalf("want 1 list, got %d (%s)", len(rows), raw)
	}

	ids, ok := rows[0]["ids"].(map[string]any)
	if !ok {
		t.Fatalf("list row has no ids object: %s", raw)
	}
	if got := ids["trakt"]; got != float64(list.ID) {
		t.Errorf("ids.trakt = %v, want %d", got, list.ID)
	}
	if got := ids["slug"]; got != "first-list" {
		t.Errorf("ids.slug = %v, want %q", got, "first-list")
	}
	if got := rows[0]["item_count"]; got != float64(1) {
		t.Errorf("item_count = %v, want 1", got)
	}
	if _, present := rows[0]["likes"]; !present {
		t.Errorf("list row has no likes: %s", raw)
	}
	user, ok := rows[0]["user"].(map[string]any)
	if !ok || user["username"] != "u-owner" {
		t.Errorf("list row has no owner user object: %s", raw)
	}
}

// TestGetListServesContractShape covers the detail route the list page loads.
func TestGetListServesContractShape(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	list := seedNativeList(t, db, "owner", "detail-list")

	status, raw := contractDo(t, nativeListsTestApp(), "owner", http.MethodGet, "/v1/lists/1", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/lists/1: want 200, got %d (%s)", status, raw)
	}
	obj := decode(t, raw)
	ids, ok := obj["ids"].(map[string]any)
	if !ok || ids["trakt"] != float64(list.ID) {
		t.Fatalf("GET /v1/lists/{id} served no usable ids: %s", raw)
	}
	if obj["item_count"] != float64(0) {
		t.Errorf("item_count = %v, want 0", obj["item_count"])
	}
}

// TestListItemsServeContractShape asserts the item object the client renders:
// nested media, and `id` = the JOIN row id (what remove/reorder accept), never
// the media id.
func TestListItemsServeContractShape(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractMedia(t, db)
	list := seedNativeList(t, db, "owner", "items-list")
	item := seedNativeItem(t, db, list.ID, 11, 0)

	status, raw := contractDo(t, nativeListsTestApp(), "owner", http.MethodGet, "/v1/lists/1/items", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/lists/1/items: want 200, got %d (%s)", status, raw)
	}
	rows := decodeArray(t, raw)
	if len(rows) != 1 {
		t.Fatalf("want 1 item, got %d (%s)", len(rows), raw)
	}
	if got := rows[0]["id"]; got != float64(item.ID) {
		t.Errorf("item id = %v, want the list_items row id %d", got, item.ID)
	}
	if got := rows[0]["type"]; got != "movie" {
		t.Errorf("item type = %v, want movie", got)
	}
	movie, ok := rows[0]["movie"].(map[string]any)
	if !ok {
		t.Fatalf("item has no nested movie object: %s", raw)
	}
	if movie["title"] != "The Matrix" {
		t.Errorf("movie.title = %v, want The Matrix", movie["title"])
	}
	movieIDs, _ := movie["ids"].(map[string]any)
	if movieIDs["trakt"] != float64(11) {
		t.Errorf("movie.ids.trakt = %v, want the local row id 11", movieIDs["trakt"])
	}
}

// TestAddListItemsAcceptsTheLocalRowID is the "Add" path the media page and the
// list detail dialog use: they hand back the `ids` object the API served them,
// whose first field is `trakt` — our own row id. The handler used to read only
// `ids.tmdb`, so the item was never added and the request still answered 201.
func TestAddListItemsAcceptsTheLocalRowID(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractMedia(t, db)
	list := seedNativeList(t, db, "owner", "add-list")

	app := nativeListsTestApp()
	status, raw := contractDo(t, app, "owner", http.MethodPost, "/v1/lists/1/items", map[string]any{
		"movies": []any{map[string]any{"ids": map[string]any{"trakt": 11}}},
	})
	if status != http.StatusCreated {
		t.Fatalf("add by trakt id: want 201, got %d (%s)", status, raw)
	}
	result := decode(t, raw)
	added, _ := result["added"].(map[string]any)
	if added["movie"] != float64(1) {
		t.Fatalf("added = %v, want movie:1 (%s)", result["added"], raw)
	}

	var count int64
	if err := db.Model(&database.ListItem{}).Where("list_id = ?", list.ID).Count(&count).Error; err != nil {
		t.Fatalf("count list items: %v", err)
	}
	if count != 1 {
		t.Fatalf("stored items = %d, want 1", count)
	}

	// idempotent: the same reference again is a duplicate, not a second row
	status, raw = contractDo(t, app, "owner", http.MethodPost, "/v1/lists/1/items", map[string]any{
		"movies": []any{map[string]any{"ids": map[string]any{"trakt": 11}}},
	})
	if status != http.StatusCreated {
		t.Fatalf("repeat add: want 201, got %d (%s)", status, raw)
	}
	if dup, _ := decode(t, raw)["duplicate"].(map[string]any); dup["movie"] != float64(1) {
		t.Errorf("repeat add should report duplicate movie:1 (%s)", raw)
	}

	// listed_at is what the client sorts "recently added" by. The column
	// defaults to now() in SQL, but GORM inserts the zero value unless the model
	// says autoCreateTime — so every added item read back as 0001-01-01.
	status, raw = contractDo(t, app, "owner", http.MethodGet, "/v1/lists/1/items", nil)
	if status != http.StatusOK {
		t.Fatalf("read back items: want 200, got %d (%s)", status, raw)
	}
	listedAt, _ := decodeArray(t, raw)[0]["listed_at"].(string)
	when, perr := time.Parse(time.RFC3339Nano, listedAt)
	if perr != nil {
		t.Fatalf("listed_at %q is not a timestamp: %v", listedAt, perr)
	}
	if time.Since(when) > time.Hour || when.After(time.Now().Add(time.Minute)) {
		t.Errorf("listed_at = %s, want the moment the item was added", listedAt)
	}
}

// TestAddListItemsStillAcceptsTMDB keeps the previous contract working: a
// client that only knows the tmdb id can still add the item.
func TestAddListItemsStillAcceptsTMDB(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractMedia(t, db)
	seedNativeList(t, db, "owner", "tmdb-list")

	status, raw := contractDo(t, nativeListsTestApp(), "owner", http.MethodPost, "/v1/lists/1/items", map[string]any{
		"movies": []any{map[string]any{"ids": map[string]any{"tmdb": 603}}},
	})
	if status != http.StatusCreated {
		t.Fatalf("add by tmdb id: want 201, got %d (%s)", status, raw)
	}
	if added, _ := decode(t, raw)["added"].(map[string]any); added["movie"] != float64(1) {
		t.Fatalf("add by tmdb id added nothing: %s", raw)
	}
}

// TestRemoveListItemsAcceptsTheLocalRowID covers unticking a list on a media
// page, which sends the same ids object back.
func TestRemoveListItemsAcceptsTheLocalRowID(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractMedia(t, db)
	list := seedNativeList(t, db, "owner", "remove-list")
	seedNativeItem(t, db, list.ID, 11, 0)

	status, raw := contractDo(t, nativeListsTestApp(), "owner", http.MethodPost, "/v1/lists/1/items/remove", map[string]any{
		"movies": []any{map[string]any{"ids": map[string]any{"trakt": 11}}},
	})
	if status != http.StatusOK {
		t.Fatalf("remove by trakt id: want 200, got %d (%s)", status, raw)
	}
	if removed := decode(t, raw)["removed"]; removed != float64(1) {
		t.Fatalf("removed = %v, want 1 (%s)", removed, raw)
	}

	var count int64
	if err := db.Model(&database.ListItem{}).Where("list_id = ?", list.ID).Count(&count).Error; err != nil {
		t.Fatalf("count list items: %v", err)
	}
	if count != 0 {
		t.Fatalf("stored items = %d, want 0", count)
	}
}

// TestListsContainingIsScopedToTheCaller covers the read the media pages use.
func TestListsContainingIsScopedToTheCaller(t *testing.T) {
	db := contractTestDB(t)
	seedContractUser(t, db, "owner")
	seedContractUser(t, db, "someone-else")
	seedContractMedia(t, db)
	mine := seedNativeList(t, db, "owner", "mine")
	other := seedNativeList(t, db, "someone-else", "theirs")
	seedNativeItem(t, db, mine.ID, 11, 0)
	seedNativeItem(t, db, other.ID, 11, 0)

	app := nativeListsTestApp()
	status, raw := contractDo(t, app, "owner", http.MethodGet, "/v1/users/me/lists/containing?type=movie&media_id=11", nil)
	if status != http.StatusOK {
		t.Fatalf("membership: want 200, got %d (%s)", status, raw)
	}
	rows := decodeArray(t, raw)
	if len(rows) != 1 {
		t.Fatalf("want only the caller's own list, got %d (%s)", len(rows), raw)
	}
	ids, _ := rows[0]["ids"].(map[string]any)
	if ids["trakt"] != float64(mine.ID) {
		t.Errorf("membership returned list %v, want %d", ids["trakt"], mine.ID)
	}

	// a title in no list of the caller is an empty array, not null
	status, raw = contractDo(t, app, "owner", http.MethodGet, "/v1/users/me/lists/containing?type=movie&media_id=999", nil)
	if status != http.StatusOK || string(raw) != "[]\n" && string(raw) != "[]" {
		t.Fatalf("empty membership: want 200 [] , got %d (%q)", status, raw)
	}

	// the pair is validated rather than silently answered []
	status, raw = contractDo(t, app, "owner", http.MethodGet, "/v1/users/me/lists/containing?type=trailer&media_id=11", nil)
	if status != http.StatusBadRequest {
		t.Errorf("bad type: want 400, got %d (%s)", status, raw)
	}
	status, raw = contractDo(t, app, "owner", http.MethodGet, "/v1/users/me/lists/containing?type=movie", nil)
	if status != http.StatusBadRequest {
		t.Errorf("missing media_id: want 400, got %d (%s)", status, raw)
	}
}
