package oauth

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/haaku-tv/haaku/internal/database"
	"github.com/haaku-tv/haaku/internal/response"
)

// ---------------------------------------------------------------------------
// GET /oauth/applications — the caller's registered applications
// ---------------------------------------------------------------------------

// applicationView is one row of the settings page: the client, plus what the
// page needs to describe a live connection.
type applicationView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	RedirectURIs  []string `json:"redirect_uris"`
	CreatedAt     string   `json:"created_at"`
	Scopes        []string `json:"scopes"`
	ActiveTokens  int      `json:"active_tokens"`
	LastIssuedAt  string   `json:"last_issued_at,omitempty"`
	AccessExpires string   `json:"access_expires_at,omitempty"`
}

type applicationListResponse struct {
	Items    []applicationView `json:"items"`
	RowCount int               `json:"row_count"`
}

// handleListApplications serves GET /oauth/applications.
//
// The settings page used to call this path and get a 405: only the POST was
// registered, so the page fell back to a hardcoded pair of imaginary clients and
// listed them as "connected". This is the read the page actually wanted.
func handleListApplications(c *fiber.Ctx) error {
	userID, err := requireAuth(c)
	if err != nil {
		return err
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "database unavailable")
	}

	// "Authorised", not "created": a third-party client is registered by one
	// account and authorised by many, and the person who approved it needs to
	// see it — and be able to revoke it — whether or not they wrote the
	// registration.
	var clients []database.OAuthClient
	listErr := db.Where(
		"user_id = ? OR id IN (?)",
		userID,
		db.Model(&database.OAuthToken{}).Select("client_id").Where("user_id = ?", userID),
	).Order("created_at DESC").Find(&clients).Error
	if listErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list applications")
	}

	items := make([]applicationView, 0, len(clients))
	for _, client := range clients {
		view := applicationView{
			ID:           client.ID,
			Name:         client.Name,
			RedirectURIs: []string(client.RedirectURIs),
			CreatedAt:    client.CreatedAt.UTC().Format(time.RFC3339),
			// Empty, never null: a client with no live token has no scopes to
			// report, and `null.length` is a crash on the page that renders it.
			Scopes: []string{},
		}

		// The newest live token describes the connection: which scopes were
		// granted and when it was last in use.
		var token database.OAuthToken
		err := db.Where("client_id = ? AND user_id = ? AND revoked = false AND expires_at > ?",
			client.ID, userID, time.Now()).
			Order("created_at DESC").First(&token).Error
		if err == nil {
			view.Scopes = []string(token.Scopes)
			view.LastIssuedAt = token.CreatedAt.UTC().Format(time.RFC3339)
			view.AccessExpires = token.ExpiresAt.UTC().Format(time.RFC3339)
			var active int64
			if err := db.Model(&database.OAuthToken{}).
				Where("client_id = ? AND user_id = ? AND revoked = false AND expires_at > ?",
					client.ID, userID, time.Now()).
				Count(&active).Error; err == nil {
				view.ActiveTokens = int(active)
			}
		}

		items = append(items, view)
	}

	return c.JSON(applicationListResponse{Items: items, RowCount: len(items)})
}

// handleDeleteApplication serves DELETE /oauth/applications/{id}.
//
// Removing an application removes its authority with it: every token issued to
// that client is revoked in the same transaction, so deleting the client cannot
// leave a credential behind that still authenticates.
func handleDeleteApplication(c *fiber.Ctx) error {
	userID, err := requireAuth(c)
	if err != nil {
		return err
	}

	clientID := strings.TrimSpace(c.Params("id"))
	if clientID == "" {
		return oauthBadRequest(c, "application id is required")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "database unavailable")
	}

	var client database.OAuthClient
	if err := db.First(&client, "id = ?", clientID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.SendStatus(fiber.StatusNoContent)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "database error")
	}
	// The registrant may delete the application outright. Anyone else who only
	// authorised it gets their own tokens revoked and the client left standing —
	// otherwise one user could delete an application that other accounts are
	// still connected to.
	if client.UserID != userID {
		if err := db.Model(&database.OAuthToken{}).
			Where("client_id = ? AND user_id = ? AND revoked = false", client.ID, userID).
			Update("revoked", true).Error; err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "failed to revoke token")
		}
		return c.SendStatus(fiber.StatusNoContent)
	}

	txErr := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.OAuthToken{}).
			Where("client_id = ? AND revoked = false", client.ID).
			Update("revoked", true).Error; err != nil {
			return err
		}
		if err := tx.Where("client_id = ?", client.ID).Delete(&database.OAuthGrant{}).Error; err != nil {
			return err
		}
		return tx.Delete(&database.OAuthClient{}, "id = ?", client.ID).Error
	})
	if txErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to delete application")
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Consent
// ---------------------------------------------------------------------------

// renderConsentPage answers /oauth/authorize with a page the reader can act on.
//
// Before this, the endpoint redirected straight back with a code: anyone who
// opened the URL in a browser had no way to see which application was asking
// for what, and no chance to refuse. The page lists the client and the scopes
// it asked for, and both answers are ordinary links, so the flow stays a GET
// and the parameters cannot be silently rewritten by a form.
func renderConsentPage(c *fiber.Ctx, client database.OAuthClient, scopes []string, params map[string]string) error {
	var approval strings.Builder
	for key, value := range params {
		if key == "decision" {
			continue
		}
		fmt.Fprintf(&approval, `<input type="hidden" name="%s" value="%s">`,
			html.EscapeString(key), html.EscapeString(value))
	}

	scopeItems := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scopeItems = append(scopeItems, fmt.Sprintf("<li>%s</li>", html.EscapeString(scope)))
	}
	if len(scopeItems) == 0 {
		scopeItems = append(scopeItems, "<li>basic access</li>")
	}

	redirectHost := html.EscapeString(params["redirect_uri"])
	page := fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Authorise %s</title>
<style>
body{font-family:system-ui,-apple-system,Segoe UI,sans-serif;background:#0b0d12;color:#e8ecf4;margin:0;display:grid;place-items:center;min-height:100vh}
main{background:#141821;border:1px solid #232a36;border-radius:16px;padding:2rem;max-width:26rem;width:calc(100%% - 3rem)}
h1{font-size:1.2rem;margin:0 0 .25rem}
p{color:#9aa4b2;font-size:.9rem;line-height:1.4}
code{color:#7dd3fc;word-break:break-all;font-size:.8rem}
ul{color:#c7d0dd;font-size:.9rem;line-height:1.6;padding-left:1.2rem}
form{margin:0}
.actions{display:flex;gap:.75rem;margin-top:1.5rem}
button{flex:1;border-radius:10px;border:1px solid #2a3341;padding:.7rem;font-size:.9rem;font-weight:600;cursor:pointer}
.approve{background:#2563eb;border-color:#2563eb;color:#fff}
.deny{background:transparent;color:#c7d0dd}
</style></head>
<body><main>
<h1>Authorise %s?</h1>
<p>This application is asking for access to your haaku account.</p>
<p>It will be sent back to <code>%s</code></p>
<p>Requested access:</p>
<ul>%s</ul>
<form method="get" action="/oauth/authorize">
%s
<div class="actions">
<button class="approve" type="submit" name="decision" value="approve">Allow</button>
<button class="deny" type="submit" name="decision" value="deny">Deny</button>
</div>
</form>
</main></body></html>`,
		html.EscapeString(client.Name), html.EscapeString(client.Name),
		redirectHost, strings.Join(scopeItems, ""), approval.String())

	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store")
	return c.Status(fiber.StatusOK).SendString(page)
}

// ---------------------------------------------------------------------------
// Revoke by application token id
// ---------------------------------------------------------------------------

type revokeByIDRequest struct {
	TokenID string `json:"token_id"`
}

// handleRevokeForUser serves POST /oauth/revoke/token: an authenticated revoke
// of one of the caller's own token rows.
//
// The public revoke endpoint takes the token itself, which the settings page
// never has — it only knows the row. Sending a token_id there was silently a
// no-op, so "revoke" on the page did nothing at all.
func handleRevokeForUser(c *fiber.Ctx) error {
	userID, err := requireAuth(c)
	if err != nil {
		return err
	}

	var req revokeByIDRequest
	if err := response.ParseBody(c, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.TokenID) == "" {
		return oauthBadRequest(c, "token_id is required")
	}

	db, dbErr := database.GetDB()
	if dbErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "database unavailable")
	}

	var token database.OAuthToken
	if err := db.First(&token, "id = ?", req.TokenID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return c.JSON(fiber.Map{"status": "ok", "revoked": false})
		}
		return fiber.NewError(fiber.StatusInternalServerError, "database error")
	}
	if token.UserID != userID {
		// Not the caller's credential: answer the same way as an unknown id
		// rather than confirming it exists.
		return c.JSON(fiber.Map{"status": "ok", "revoked": false})
	}

	if err := db.Model(&database.OAuthToken{}).
		Where("id = ?", token.ID).
		Update("revoked", true).Error; err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to revoke token")
	}
	return c.JSON(fiber.Map{"status": "ok", "revoked": true})
}
