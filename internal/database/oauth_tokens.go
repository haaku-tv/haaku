package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrOAuthTokenInvalid covers every reason an OAuth access token cannot be
// used: unknown, revoked, expired, or belonging to an account that is not
// active. The caller answers the same way to all of them, so the response never
// says which.
var ErrOAuthTokenInvalid = errors.New("oauth access token is not valid")

// OAuthAccessToken is what a live access token resolves to.
type OAuthAccessToken struct {
	UserID    string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

// ResolveOAuthAccessToken turns a hashed access token into its account.
//
// The token is looked up by hash: the plaintext is only ever held by the client
// it was issued to, never stored. A row that is revoked or past its expiry is
// rejected here rather than at the call sites, so every surface that accepts an
// OAuth token inherits the same rules.
func ResolveOAuthAccessToken(db *gorm.DB, tokenHash string) (*OAuthAccessToken, error) {
	if db == nil || tokenHash == "" {
		return nil, ErrOAuthTokenInvalid
	}

	var token OAuthToken
	if err := db.Where("token_hash = ?", tokenHash).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOAuthTokenInvalid
		}
		return nil, err
	}
	if token.Revoked {
		return nil, ErrOAuthTokenInvalid
	}
	if !token.ExpiresAt.IsZero() && time.Now().After(token.ExpiresAt) {
		return nil, ErrOAuthTokenInvalid
	}

	// The account behind the token must still be usable: a disabled or deleted
	// owner revokes the token's authority without needing a revocation row.
	var user User
	if err := db.Select("id", "status").First(&user, "id = ?", token.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrOAuthTokenInvalid
		}
		return nil, err
	}
	if user.Status != "" && user.Status != "active" {
		return nil, ErrOAuthTokenInvalid
	}

	return &OAuthAccessToken{
		UserID:    token.UserID,
		ClientID:  token.ClientID,
		Scopes:    []string(token.Scopes),
		ExpiresAt: token.ExpiresAt,
	}, nil
}
