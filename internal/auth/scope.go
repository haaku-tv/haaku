package auth

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// OAuth token scope enforcement.
//
// An OAuth access token is issued for a set of scopes, and until this guard
// existed those scopes were decoration: the middleware resolved the account and
// threw the scopes away, so a token granted only "profile" could write history,
// rewrite lists and follow people. A token must never carry more authority than
// the person who approved it was shown.
//
// The rule table below maps a path prefix to the read and write scope it needs.
// It is deliberately coarse — a prefix per surface, not a permission per
// handler — because the honest failure mode here is "refuse the call and say
// which scope is missing", not "guess". Anything not in the table needs
// `profile` to read and is refused for a write, so a surface added later is
// closed by default rather than silently open.

type scopeRule struct {
	prefix string
	read   string
	write  string
}

var scopeRules = []scopeRule{
	{"/v1/sync/history", "history:read", "history:write"},
	{"/v1/sync/collection", "history:read", "history:write"},
	{"/v1/sync/watchlist", "watchlist:read", "watchlist:write"},
	{"/v1/sync/favorites", "watchlist:read", "watchlist:write"},
	{"/v1/sync/ratings", "ratings:read", "ratings:write"},
	{"/v1/sync/progress", "history:read", "history:write"},
	{"/v1/lists", "lists:read", "lists:write"},
	{"/v1/users/me", "profile", "profile"},
	{"/v1/oauth", "profile", "profile"},
}

// profileScope is what an unmapped read needs: the token identifies an account
// and may see that account's own data.
const profileScope = "profile"

// OAuthScopeGuard refuses an OAuth-authenticated request whose token does not
// hold the scope the endpoint needs.
//
// Session, JWT and API-key callers pass through untouched: this is the rule set
// for delegated tokens, not a second authorization layer for the account owner.
func OAuthScopeGuard() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Resolve the caller here rather than relying on an earlier middleware:
		// a guard that only works when something else ran first is a guard that
		// silently stops working the day the order changes.
		_, _ = AttemptAuth(c)

		method, _ := c.Locals(ctxAuthMethod).(string)
		if method != "oauth" {
			return c.Next()
		}

		scopes, _ := c.Locals(ctxOAuthScopes).([]string)
		needed, ok := requiredScope(c.Path(), c.Method())
		if !ok {
			return writeError(c, fiber.StatusForbidden, "insufficient_scope",
				"this OAuth token cannot be used on "+c.Method()+" "+c.Path())
		}
		if hasScope(scopes, needed) {
			return c.Next()
		}
		return writeError(c, fiber.StatusForbidden, "insufficient_scope",
			"this OAuth token needs the "+needed+" scope for "+c.Method()+" "+c.Path())
	}
}

// requiredScope resolves the scope a request needs, and whether the rule set
// covers it at all.
func requiredScope(path, method string) (string, bool) {
	read := method == fiber.MethodGet || method == fiber.MethodHead || method == fiber.MethodOptions

	// Longest prefix wins, so a nested surface can be stricter than its parent.
	var best *scopeRule
	for i := range scopeRules {
		rule := &scopeRules[i]
		if !strings.HasPrefix(path, rule.prefix) {
			continue
		}
		if best == nil || len(rule.prefix) > len(best.prefix) {
			best = rule
		}
	}

	if best != nil {
		if read {
			return best.read, true
		}
		return best.write, true
	}
	if read {
		return profileScope, true
	}
	// An unmapped write has no scope that can authorise it. Closing it is the
	// safe direction: a new surface is refused to delegated tokens until it is
	// given a rule.
	return "", false
}

// hasScope reports whether the token holds the scope. A write scope implies its
// read counterpart, which is how the scope list is advertised at consent time.
func hasScope(scopes []string, needed string) bool {
	for _, scope := range scopes {
		if scope == needed {
			return true
		}
		if strings.HasSuffix(scope, ":write") && scope == strings.TrimSuffix(needed, ":read")+":write" {
			return true
		}
		if scope == "*" {
			return true
		}
	}
	return false
}
