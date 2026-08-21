// Package pgstore implements store.Store on PostgreSQL (pgx stdlib driver).
// Compile-checked in CI; exercised via docker compose (see docker-compose.yml).
package pgstore

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/opendiscord/opendiscord/internal/store"
)

//go:embed schema.sql
var schema string

type PG struct{ db *sql.DB }

func Open(ctx context.Context, dsn string) (*PG, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &PG{db: db}, nil
}

func (p *PG) Close() error { return p.db.Close() }

func toI64(id string) int64 {
	n, _ := strconv.ParseInt(id, 10, 64)
	return n
}

func fromI64(n int64) string { return strconv.FormatInt(n, 10) }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (p *PG) CreateUser(ctx context.Context, u store.User) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO users (id, username, display_name, avatar, pass_hash, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		toI64(u.ID), u.Username, u.DisplayName, u.Avatar, u.PassHash, u.CreatedAt)
	if isUniqueViolation(err) {
		return store.ErrConflict
	}
	return err
}

func (p *PG) scanUser(row *sql.Row) (store.User, error) {
	var u store.User
	var id int64
	err := row.Scan(&id, &u.Username, &u.DisplayName, &u.Avatar, &u.PassHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.User{}, store.ErrNotFound
	}
	u.ID = fromI64(id)
	return u, err
}

func (p *PG) UserByName(ctx context.Context, username string) (store.User, error) {
	return p.scanUser(p.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, avatar, pass_hash, created_at
		 FROM users WHERE LOWER(username) = LOWER($1)`, username))
}

func (p *PG) UserByID(ctx context.Context, id string) (store.User, error) {
	return p.scanUser(p.db.QueryRowContext(ctx,
		`SELECT id, username, display_name, avatar, pass_hash, created_at
		 FROM users WHERE id = $1`, toI64(id)))
}

func (p *PG) CreateToken(ctx context.Context, token, userID string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO tokens (token, user_id) VALUES ($1,$2)`, token, toI64(userID))
	return err
}

func (p *PG) UserIDByToken(ctx context.Context, token string) (string, error) {
	var id int64
	err := p.db.QueryRowContext(ctx,
		`SELECT user_id FROM tokens WHERE token = $1`, token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return fromI64(id), err
}

func (p *PG) CreateGuild(ctx context.Context, g store.Guild) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO guilds (id, name, owner_id, created_at) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (id) DO NOTHING`,
		toI64(g.ID), g.Name, toI64(g.OwnerID), g.CreatedAt)
	return err
}

func (p *PG) Guilds(ctx context.Context) ([]store.Guild, error) {
	return p.scanGuilds(ctx, `SELECT id, name, owner_id, created_at FROM guilds ORDER BY id`)
}

func (p *PG) scanGuilds(ctx context.Context, q string, args ...any) ([]store.Guild, error) {
	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Guild
	for rows.Next() {
		var g store.Guild
		var id, owner int64
		if err := rows.Scan(&id, &g.Name, &owner, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.ID, g.OwnerID = fromI64(id), fromI64(owner)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *PG) AddGuildMember(ctx context.Context, guildID, userID string) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO guild_members (guild_id, user_id) VALUES ($1,$2)
		 ON CONFLICT DO NOTHING`, toI64(guildID), toI64(userID))
	return err
}

func (p *PG) GuildsByUser(ctx context.Context, userID string) ([]store.Guild, error) {
	return p.scanGuilds(ctx,
		`SELECT g.id, g.name, g.owner_id, g.created_at
		 FROM guilds g JOIN guild_members m ON m.guild_id = g.id
		 WHERE m.user_id = $1 ORDER BY g.id`, toI64(userID))
}

func (p *PG) CreateChannel(ctx context.Context, c store.Channel) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO channels (id, guild_id, type, name, topic, position, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		toI64(c.ID), toI64(c.GuildID), int(c.Type), c.Name, c.Topic, c.Position, c.CreatedAt)
	return err
}

func (p *PG) ChannelByID(ctx context.Context, id string) (store.Channel, error) {
	var c store.Channel
	var cid, gid int64
	var typ int
	err := p.db.QueryRowContext(ctx,
		`SELECT id, guild_id, type, name, topic, position, created_at
		 FROM channels WHERE id = $1`, toI64(id)).
		Scan(&cid, &gid, &typ, &c.Name, &c.Topic, &c.Position, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Channel{}, store.ErrNotFound
	}
	c.ID, c.GuildID, c.Type = fromI64(cid), fromI64(gid), store.ChannelType(typ)
	return c, err
}

func (p *PG) ChannelsByGuild(ctx context.Context, guildID string) ([]store.Channel, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, guild_id, type, name, topic, position, created_at
		 FROM channels WHERE guild_id = $1 ORDER BY position, id`, toI64(guildID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Channel
	for rows.Next() {
		var c store.Channel
		var cid, gid int64
		var typ int
		if err := rows.Scan(&cid, &gid, &typ, &c.Name, &c.Topic, &c.Position, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.ID, c.GuildID, c.Type = fromI64(cid), fromI64(gid), store.ChannelType(typ)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *PG) CreateMessage(ctx context.Context, m store.Message) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO messages (id, channel_id, author_id, content, created_at, edited_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		toI64(m.ID), toI64(m.ChannelID), toI64(m.Author.ID), m.Content, m.CreatedAt, m.EditedAt)
	return err
}

func (p *PG) MessagesByChannel(ctx context.Context, channelID, before string, limit int) ([]store.Message, error) {
	q := `SELECT m.id, m.channel_id, m.content, m.created_at, m.edited_at,
	             u.id, u.username, u.display_name, u.avatar, u.created_at
	      FROM messages m JOIN users u ON u.id = m.author_id
	      WHERE m.channel_id = $1`
	args := []any{toI64(channelID)}
	if before != "" {
		q += ` AND m.id < $2`
		args = append(args, toI64(before))
	}
	q += fmt.Sprintf(` ORDER BY m.id DESC LIMIT %d`, limit)
	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.Message
	for rows.Next() {
		var m store.Message
		var mid, cid, uid int64
		if err := rows.Scan(&mid, &cid, &m.Content, &m.CreatedAt, &m.EditedAt,
			&uid, &m.Author.Username, &m.Author.DisplayName, &m.Author.Avatar, &m.Author.CreatedAt); err != nil {
			return nil, err
		}
		m.ID, m.ChannelID, m.Author.ID = fromI64(mid), fromI64(cid), fromI64(uid)
		out = append(out, m)
	}
	return out, rows.Err()
}
