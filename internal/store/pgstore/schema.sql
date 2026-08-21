-- OpenDiscord schema, applied idempotently at startup (v0; real migrations at M1).
-- IDs are snowflakes stored as BIGINT; the API layer converts to/from strings.

CREATE TABLE IF NOT EXISTS users (
    id           BIGINT PRIMARY KEY,
    username     TEXT NOT NULL,
    display_name TEXT NOT NULL,
    avatar       TEXT,
    pass_hash    BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower ON users (LOWER(username));

CREATE TABLE IF NOT EXISTS tokens (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS guilds (
    id         BIGINT PRIMARY KEY,
    name       TEXT NOT NULL,
    owner_id   BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
    id         BIGINT PRIMARY KEY,
    guild_id   BIGINT NOT NULL REFERENCES guilds (id) ON DELETE CASCADE,
    type       INT NOT NULL,
    name       TEXT NOT NULL,
    topic      TEXT,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS channels_guild ON channels (guild_id, position, id);

-- Partitioning by (channel_id, time) comes with volume (PLANNING.md §3.2);
-- a plain table with this index is fine far past v0.
CREATE TABLE IF NOT EXISTS messages (
    id         BIGINT PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    author_id  BIGINT NOT NULL REFERENCES users (id),
    content    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    edited_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS messages_channel_id_desc ON messages (channel_id, id DESC);
