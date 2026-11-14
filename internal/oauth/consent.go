package oauth

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/haaku-tv/haaku/internal/database"
)

// authorizeRequest is the parameter set a consent decision is made about. The
// consent page carries these back verbatim, so the approval a reader clicks
// applies to exactly the request they were shown.
type authorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

// consentGate decides whether the authorization request may proceed.
//
// It returns handled=true when it has already answered the request: either with
// the consent page (no decision yet) or with a refusal that sends the client
// back to its own redirect_uri. handled=false means the owner approved and the
// caller should issue the code.
func consentGate(c *fiber.Ctx, client database.OAuthClient, req authorizeRequest) (bool, error) {
	decision := strings.ToLower(strings.TrimSpace(c.Query("decision")))
	if decision == "" {
		scopes := validateScopes(parseScopeList(req.Scope))
		params := map[string]string{
			"response_type":         req.ResponseType,
			"client_id":             req.ClientID,
			"redirect_uri":          req.RedirectURI,
			"scope":                 req.Scope,
			"state":                 req.State,
			"code_challenge":        req.CodeChallenge,
			"code_challenge_method": req.CodeChallengeMethod,
		}
		return true, renderConsentPage(c, client, scopes, params)
	}
	if decision == "approve" {
		return false, nil
	}

	// Any answer other than "approve" is a refusal. The client is told through
	// its own redirect_uri, with the state it sent, so it can show the reader
	// what happened instead of hanging on a spinner.
	target, err := url.Parse(req.RedirectURI)
	if err != nil {
		return true, oauthBadRequest(c, "invalid redirect_uri")
	}
	params := target.Query()
	params.Set("error", "access_denied")
	params.Set("error_description", "the account owner declined the request")
	if req.State != "" {
		params.Set("state", req.State)
	}
	target.RawQuery = params.Encode()
	return true, c.Redirect(target.String(), fiber.StatusFound)
}

// parseScopeList splits a space- or comma-separated scope string.
func parseScopeList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

func validateScopes(requested []string) []string {
	if len(requested) == 0 {
		return defaultScopes
	}
	seen := make(map[string]bool)
	var result []string
	for _, s := range requested {
		s = strings.TrimSpace(s)
		if validScopes[s] && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return defaultScopes
	}
	return result
}

func scopeString(scopes []string) string {
	return strings.Join(scopes, " ")
}
