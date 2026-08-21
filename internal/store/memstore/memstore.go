// Package memstore is the in-memory Store used for development and tests.
// Data is lost on restart; use pgstore for anything real.
package memstore

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/opendiscord/opendiscord/internal/store"
)

type Mem struct {
	mu       sync.RWMutex
	users    map[string]store.User // by ID
	byName   map[string]string     // lower(username) -> ID
	tokens   map[string]string     // token -> userID
	guilds   map[string]store.Guild
	members  map[string]map[string]struct{} // guildID -> set of userID
	channels map[string]store.Channel
	messages map[string][]store.Message // channelID -> ascending by ID
}

func New() *Mem {
	return &Mem{
		users:    map[string]store.User{},
		byName:   map[string]string{},
		tokens:   map[string]string{},
		guilds:   map[string]store.Guild{},
		members:  map[string]map[string]struct{}{},
		channels: map[string]store.Channel{},
		messages: map[string][]store.Message{},
	}
}

func (m *Mem) CreateUser(_ context.Context, u store.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(u.Username)
	if _, ok := m.byName[key]; ok {
		return store.ErrConflict
	}
	m.users[u.ID] = u
	m.byName[key] = u.ID
	return nil
}

func (m *Mem) UserByName(_ context.Context, username string) (store.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byName[strings.ToLower(username)]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return m.users[id], nil
}

func (m *Mem) UserByID(_ context.Context, id string) (store.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

func (m *Mem) CreateToken(_ context.Context, token, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token] = userID
	return nil
}

func (m *Mem) UserIDByToken(_ context.Context, token string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.tokens[token]
	if !ok {
		return "", store.ErrNotFound
	}
	return id, nil
}

func (m *Mem) CreateGuild(_ context.Context, g store.Guild) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guilds[g.ID] = g
	return nil
}

func (m *Mem) Guilds(_ context.Context) ([]store.Guild, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]store.Guild, 0, len(m.guilds))
	for _, g := range m.guilds {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

func (m *Mem) AddGuildMember(_ context.Context, guildID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.guilds[guildID]; !ok {
		return store.ErrNotFound
	}
	if m.members[guildID] == nil {
		m.members[guildID] = map[string]struct{}{}
	}
	m.members[guildID][userID] = struct{}{}
	return nil
}

func (m *Mem) GuildsByUser(_ context.Context, userID string) ([]store.Guild, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]store.Guild, 0, len(m.guilds))
	for id, g := range m.guilds {
		if _, ok := m.members[id][userID]; ok {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	return out, nil
}

func (m *Mem) CreateChannel(_ context.Context, c store.Channel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[c.ID] = c
	return nil
}

func (m *Mem) ChannelByID(_ context.Context, id string) (store.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.channels[id]
	if !ok {
		return store.Channel{}, store.ErrNotFound
	}
	return c, nil
}

func (m *Mem) ChannelsByGuild(_ context.Context, guildID string) ([]store.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []store.Channel
	for _, c := range m.channels {
		if c.GuildID == guildID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return idLess(out[i].ID, out[j].ID)
	})
	return out, nil
}

func (m *Mem) CreateMessage(_ context.Context, msg store.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[msg.ChannelID] = append(m.messages[msg.ChannelID], msg)
	return nil
}

func (m *Mem) MessagesByChannel(_ context.Context, channelID, before string, limit int) ([]store.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := m.messages[channelID]
	out := make([]store.Message, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if before != "" && !idLess(all[i].ID, before) {
			continue
		}
		out = append(out, all[i])
	}
	return out, nil
}

// idLess compares decimal snowflake strings numerically.
func idLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
