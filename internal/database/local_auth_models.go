package database

import "time"

// Local-account models (issue #53).
//
// Identity for a self-hosted instance is issued by this server: a users row
// whose id is prefixed "local_" (never a Clerk id), a password hash in
// local_credentials, and revocable server-side sessions in local_sessions.
// The Clerk mirror columns on users are untouched by this module, and a Clerk
// deployment never writes these tables — see migrations/sql/*_local_auth.sql.

// users.status values written by the local account module. The column already
// existed (default 'active'); local auth is its first writer besides the Clerk
// sync. "banned" is what an admin lock sets; a banned account cannot log in and
// its live sessions are revoked.
const (
	UserStatusActive  = "active"
	UserStatusPending = "pending" // signed up, waiting for an admin approval
	UserStatusBanned  = "banned"  // banned/locked by an admin
)

// LocalCredential is the password hash for one local account. Exactly one row
// per local user; a Clerk user has no row here.
type LocalCredential struct {
	UserID            string    `gorm:"primaryKey;type:text" json:"user_id"`
	PasswordHash      string    `gorm:"type:text;not null" json:"-"`
	PasswordUpdatedAt time.Time `json:"password_updated_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func (LocalCredential) TableName() string { return "local_credentials" }

// LocalSession is one opaque server-side session. TokenHash is the HMAC blind
// index of the bearer token (the token itself is never stored), so a row can be
// revoked, and every row for a user can be revoked in one statement.
type LocalSession struct {
	ID         string     `gorm:"primaryKey;type:text" json:"id"`
	UserID     string     `gorm:"type:text;not null;index" json:"user_id"`
	TokenHash  string     `gorm:"type:text;not null;uniqueIndex" json:"-"`
	ExpiresAt  time.Time  `gorm:"not null;index" json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	IP         string     `gorm:"type:text" json:"ip,omitempty"`
	UserAgent  string     `gorm:"type:text" json:"user_agent,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (LocalSession) TableName() string { return "local_sessions" }

// APIKey is one `tk_` bearer credential minted by this server for a local
// account (issue #92). KeyHash is the HMAC blind index of the raw key under an
// HKDF-derived lookup key — the raw key is returned once at creation and is not
// recoverable from this row. A Clerk deployment keeps its keys in Clerk instead
// and never writes this table.
type APIKey struct {
	ID         string     `gorm:"primaryKey;type:text" json:"id"`
	UserID     string     `gorm:"type:text;not null;index" json:"user_id"`
	Prefix     string     `gorm:"type:text;not null" json:"prefix"`
	KeyHash    string     `gorm:"type:text;not null;uniqueIndex" json:"-"`
	Name       string     `gorm:"type:text;not null;default:''" json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (APIKey) TableName() string { return "api_keys" }

// LocalAuthToken is a one-time token an admin issues and hands to a user
// (password reset). ConsumedAt makes redemption single-use.
type LocalAuthToken struct {
	ID         string     `gorm:"primaryKey;type:text" json:"id"`
	UserID     string     `gorm:"type:text;not null;index" json:"user_id"`
	TokenHash  string     `gorm:"type:text;not null;uniqueIndex" json:"-"`
	Purpose    string     `gorm:"type:text;not null;default:password_reset" json:"purpose"`
	CreatedBy  string     `gorm:"type:text" json:"created_by,omitempty"`
	ExpiresAt  time.Time  `gorm:"not null" json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (LocalAuthToken) TableName() string { return "local_auth_tokens" }

// LocalAuthTokenPurposePasswordReset is the only purpose issued today.
const LocalAuthTokenPurposePasswordReset = "password_reset"
