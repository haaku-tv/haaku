-- +goose Up
-- Local API keys (issue #92): the `tk_` credential a self-hosted instance can
-- mint for itself.
--
-- In clerk mode keys live in Clerk's `unsafe_metadata.api_keys` and are only
-- ever touched through the Clerk Backend API. A local instance has no Clerk
-- tenant to talk to, so the same credential is issued and looked up here
-- instead. Append-only and additive: a Clerk deployment simply never creates a
-- row in this table, exactly like local_credentials / local_sessions.
--
-- The raw key is never stored. `key_hash` is an HMAC-SHA256 blind index keyed
-- by HKDF-SHA256(secret.HAAKU_SECRET_KEY, "haaku/api-key-token/v1") — the same
-- root key and purpose-labelling convention as local_sessions, under its own
-- domain label so the two can never be cross-used. A database dump without the
-- root key is therefore not a set of usable credentials, and the column can
-- still be matched with an equality query under a unique index (a
-- random-nonce AES-GCM ciphertext could not be).
--
-- `prefix` is the display/cache half of the key (`tk_` + the first six hex
-- characters). It is deliberately NOT unique: it is truncated, so a collision
-- is possible, and the hash is what identifies a key.
CREATE TABLE IF NOT EXISTS api_keys (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prefix       TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys (user_id);

-- +goose Down
DROP TABLE IF EXISTS api_keys;
