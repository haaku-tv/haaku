package contractshape

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/haaku-tv/haaku/internal/database"
)

// TestUserEmitsAnIntegerTraktID pins the shape the contract requires of the
// embedded user object: `ids` has exactly two properties, `slug` and `trakt`, and
// `trakt` is a required *integer*.
//
// It used to be null, and the object also carried a `uuid` the contract does not
// declare there. A null in a required integer field makes a conforming client
// reject the whole feed, so this is not a cosmetic difference — and the numeric
// value has to come from somewhere real (see LocalUserNum), not from a constant.
func TestUserEmitsAnIntegerTraktID(t *testing.T) {
	u := &database.User{ID: "local_alice", Username: "alice", Name: "Alice"}
	got := User(u)

	ids, ok := got["ids"].(map[string]any)
	if !ok {
		t.Fatalf("ids is %#v, want an object", got["ids"])
	}
	n, ok := ids["trakt"].(int64)
	if !ok {
		t.Fatalf("ids.trakt = %#v (%T), want an integer", ids["trakt"], ids["trakt"])
	}
	if n <= 0 {
		t.Fatalf("ids.trakt = %d, want a positive number", n)
	}
	if _, has := ids["uuid"]; has {
		t.Fatalf("ids carries uuid %v; the contract's embedded user ids are slug+trakt only", ids["uuid"])
	}
	if ids["slug"] != "alice" {
		t.Fatalf("ids.slug = %#v, want the username", ids["slug"])
	}
}

// TestLocalUserNumIsStableAndDistinct keeps the derivation honest: the same
// account must get the same number in every response, and two accounts must not
// collide on the ids a client keys by.
func TestLocalUserNumIsStableAndDistinct(t *testing.T) {
	if a, b := LocalUserNum("local_alice"), LocalUserNum("local_alice"); a != b {
		t.Fatalf("LocalUserNum is not deterministic: %d != %d", a, b)
	}
	seen := map[int64]string{}
	for _, id := range []string{"local_alice", "local_bob", "user_2abc", "user_2abd", ""} {
		n := LocalUserNum(id)
		if prev, dup := seen[n]; dup {
			t.Fatalf("LocalUserNum(%q) and LocalUserNum(%q) both = %d", id, prev, n)
		}
		seen[n] = id
		if n < 0 {
			t.Fatalf("LocalUserNum(%q) = %d, want a positive id", id, n)
		}
	}
}

// TestUserNilIsMarkedDeleted covers the not-found render: it still has to carry
// the required integer, so a client can parse the row it was told about.
func TestUserNilIsMarkedDeleted(t *testing.T) {
	got := User(nil)
	if got["deleted"] != true {
		t.Fatalf("deleted = %#v, want true", got["deleted"])
	}
	ids := got["ids"].(map[string]any)
	if _, ok := ids["trakt"].(int64); !ok {
		t.Fatalf("ids.trakt = %#v, want an integer even for a deleted user", ids["trakt"])
	}
}

// mediaImageKeySet is the contract's required image keys, copied from
// docs/parity/upstream/trakt-openapi.json. It is duplicated here on purpose: a
// test that reads the map it is testing proves nothing.
var mediaImageKeySet = map[string][]string{
	"movie":   {"fanart", "poster", "logo", "clearart", "banner", "thumb"},
	"show":    {"fanart", "poster", "logo", "clearart", "banner", "thumb"},
	"season":  {"poster", "thumb"},
	"episode": {"screenshot"},
	"person":  {"headshot", "fanart"},
}

// TestMediaImagesCarryEveryRequiredKey is the #140 acceptance test: every media
// type's images object carries its full required key set, as arrays, even for a
// row that holds no images at all. A missing key is a conformance failure
// (`images.logo: missing required field` was the first diff on 44 operations),
// and a null would be rejected by the contract's ["array"] item type just as
// hard — so the assertions are on the key set and the JSON type, not the values.
func TestMediaImagesCarryEveryRequiredKey(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		stored database.JSONMap
		// expect maps a key to the URLs it must resolve to, for the rows that
		// hold data under a provider's own key name.
		expect map[string][]string
	}{
		{name: "movie with nothing stored", kind: "movie"},
		{name: "show with nothing stored", kind: "show"},
		{name: "season with nothing stored", kind: "season"},
		{name: "episode with nothing stored", kind: "episode"},
		{name: "person with nothing stored", kind: "person"},
		{
			name:   "movie resolves poster and maps backdrop onto fanart",
			kind:   "movie",
			stored: database.JSONMap{"poster": "https://example.test/p.jpg", "backdrop": "https://example.test/b.jpg"},
			expect: map[string][]string{
				"poster": {"https://example.test/p.jpg"},
				"fanart": {"https://example.test/b.jpg"},
			},
		},
		{
			name:   "show resolves a bare TMDB path and a tvdb poster",
			kind:   "show",
			stored: database.JSONMap{"poster_path": "/abc.jpg"},
			expect: map[string][]string{"poster": {"https://image.tmdb.org/t/p/w500/abc.jpg"}},
		},
		{
			name:   "episode maps its still onto screenshot",
			kind:   "episode",
			stored: database.JSONMap{"still": "https://example.test/s.jpg"},
			expect: map[string][]string{"screenshot": {"https://example.test/s.jpg"}},
		},
		{
			name:   "person maps a raw profile path onto headshot",
			kind:   "person",
			stored: database.JSONMap{"profile_path": "/who.jpg"},
			expect: map[string][]string{"headshot": {"https://image.tmdb.org/t/p/w500/who.jpg"}},
		},
		{
			name:   "a stored array round-tripped through JSONB is preserved",
			kind:   "movie",
			stored: database.JSONMap{"poster": []any{"https://example.test/1.jpg", "https://example.test/2.jpg"}},
			expect: map[string][]string{"poster": {"https://example.test/1.jpg", "https://example.test/2.jpg"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MediaImages(tc.kind, tc.stored)

			want := mediaImageKeySet[tc.kind]
			if len(got) != len(want) {
				t.Errorf("%s images has %d keys, want exactly %d: %v", tc.kind, len(got), len(want), got)
			}
			for _, key := range want {
				raw, ok := got[key]
				if !ok {
					t.Errorf("%s images.%s missing — the contract requires it", tc.kind, key)
					continue
				}
				// Must be a real array, never nil (null) and never a bare string.
				values, ok := raw.([]string)
				if !ok {
					t.Errorf("%s images.%s is %T, want []string (the contract's item type is an array)", tc.kind, key, raw)
					continue
				}
				if wantURLs, checked := tc.expect[key]; checked {
					if !reflect.DeepEqual(values, wantURLs) {
						t.Errorf("%s images.%s = %v, want %v", tc.kind, key, values, wantURLs)
					}
				}
			}

			// And the whole thing must survive JSON as arrays, not nulls.
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(encoded), "null") {
				t.Errorf("images marshalled with a null value: %s", encoded)
			}
		})
	}
}
