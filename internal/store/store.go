// Package store defines the persistence interface and the wire-level object
// model (docs/PROTOCOL.md §2). Implementations: memstore (dev/tests),
// pgstore (production, docker compose).
package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Avatar      *string   `json:"avatar"`
	CreatedAt   time.Time `json:"created_at"`
	PassHash    []byte    `json:"-"`
}

type Guild struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

type ChannelType int

const (
	ChannelText ChannelType = 0
	// Reserved: 1 DM, 2 voice, 3 category, 4 announcement, 5 forum.
)

type Channel struct {
	ID        string      `json:"id"`
	GuildID   string      `json:"guild_id"`
	Type      ChannelType `json:"type"`
	Name      string      `json:"name"`
	Topic     *string     `json:"topic"`
	Position  int         `json:"position"`
	CreatedAt time.Time   `json:"created_at"`
}

type Message struct {
	ID        string     `json:"id"`
	ChannelID string     `json:"channel_id"`
	Author    User       `json:"author"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	EditedAt  *time.Time `json:"edited_at"`
}

type Store interface {
	// Users. CreateUser returns ErrConflict on duplicate username.
	CreateUser(ctx context.Context, u User) error
	UserByName(ctx context.Context, username string) (User, error)
	UserByID(ctx context.Context, id string) (User, error)

	// Tokens (opaque bearer tokens; value stored hashed by caller if desired).
	CreateToken(ctx context.Context, token, userID string) error
	UserIDByToken(ctx context.Context, token string) (string, error)

	// Guilds. v0: one default guild, everyone is a member (PROTOCOL §2).
	CreateGuild(ctx context.Context, g Guild) error
	Guilds(ctx context.Context) ([]Guild, error)

	// Channels.
	CreateChannel(ctx context.Context, c Channel) error
	ChannelByID(ctx context.Context, id string) (Channel, error)
	ChannelsByGuild(ctx context.Context, guildID string) ([]Channel, error)

	// Messages. List returns newest-first, filtered by before (exclusive) if set.
	CreateMessage(ctx context.Context, m Message) error
	MessagesByChannel(ctx context.Context, channelID, before string, limit int) ([]Message, error)
}
