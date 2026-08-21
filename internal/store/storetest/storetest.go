// Package storetest holds one behavioural suite that every store.Store
// implementation must satisfy. memstore and pgstore are meant to be
// interchangeable; the only way that stays true is if the same assertions run
// against both.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/KinonNeko/openorder/internal/store"
)

// Run executes the suite. newStore must return a store with no data in it;
// it is called once per subtest so cases cannot leak into each other.
func Run(t *testing.T, newStore func(t *testing.T) store.Store) {
	t.Helper()
	for _, tc := range []struct {
		name string
		fn   func(*testing.T, store.Store)
	}{
		{"Users", testUsers},
		{"Tokens", testTokens},
		{"GuildMembership", testGuildMembership},
		{"Channels", testChannels},
		{"MessagePaging", testMessagePaging},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, newStore(t)) })
	}
}

// ids yields snowflake-shaped decimal strings that sort the way real ones do.
func id(n int) string { return fmt.Sprintf("%d", 48291057123328+n) }

func user(n int, name string) store.User {
	return store.User{
		ID: id(n), Username: name, DisplayName: name,
		PassHash: []byte("hash"), CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func testUsers(t *testing.T, s store.Store) {
	ctx := context.Background()
	u := user(1, "alice")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Duplicate usernames must conflict, and must do so case-insensitively --
	// otherwise "Alice" and "alice" become two accounts users cannot tell apart.
	dup := user(2, "ALICE")
	if err := s.CreateUser(ctx, dup); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate username err = %v, want ErrConflict", err)
	}
	got, err := s.UserByName(ctx, "AlIcE")
	if err != nil {
		t.Fatalf("UserByName is case-insensitive: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("UserByName id = %s, want %s", got.ID, u.ID)
	}
	if got, err = s.UserByID(ctx, u.ID); err != nil || got.Username != "alice" {
		t.Fatalf("UserByID = %+v, %v", got, err)
	}
	if _, err := s.UserByID(ctx, id(99)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
}

func testTokens(t *testing.T, s store.Store) {
	ctx := context.Background()
	u := user(1, "alice")
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, "oot_abc", u.ID); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	uid, err := s.UserIDByToken(ctx, "oot_abc")
	if err != nil || uid != u.ID {
		t.Fatalf("UserIDByToken = %q, %v", uid, err)
	}
	if _, err := s.UserIDByToken(ctx, "oot_nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown token err = %v, want ErrNotFound", err)
	}
}

func testGuildMembership(t *testing.T, s store.Store) {
	ctx := context.Background()
	alice, bob := user(1, "alice"), user(2, "bob")
	for _, u := range []store.User{alice, bob} {
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	g1 := store.Guild{ID: id(10), Name: "one", OwnerID: alice.ID, CreatedAt: time.Now().UTC()}
	g2 := store.Guild{ID: id(11), Name: "two", OwnerID: alice.ID, CreatedAt: time.Now().UTC()}
	for _, g := range []store.Guild{g1, g2} {
		if err := s.CreateGuild(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	// A guild with no members must not show up for anyone: membership is an
	// explicit relation, not "every user is in every guild" (PROTOCOL §2).
	if got, err := s.GuildsByUser(ctx, alice.ID); err != nil || len(got) != 0 {
		t.Fatalf("GuildsByUser before joining = %+v, %v; want empty", got, err)
	}

	if err := s.AddGuildMember(ctx, g1.ID, alice.ID); err != nil {
		t.Fatalf("AddGuildMember: %v", err)
	}
	if err := s.AddGuildMember(ctx, g1.ID, alice.ID); err != nil {
		t.Fatalf("AddGuildMember must be idempotent: %v", err)
	}
	if err := s.AddGuildMember(ctx, g2.ID, bob.ID); err != nil {
		t.Fatal(err)
	}

	assertGuilds := func(userID string, want ...string) {
		t.Helper()
		got, err := s.GuildsByUser(ctx, userID)
		if err != nil {
			t.Fatalf("GuildsByUser: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("GuildsByUser = %d guilds, want %d (%+v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i].ID != want[i] {
				t.Fatalf("guild[%d] = %s, want %s", i, got[i].ID, want[i])
			}
		}
	}
	assertGuilds(alice.ID, g1.ID)
	assertGuilds(bob.ID, g2.ID)

	if err := s.AddGuildMember(ctx, g2.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	assertGuilds(alice.ID, g1.ID, g2.ID) // ascending by ID

	// The instance-wide listing is a different question from membership.
	all, err := s.Guilds(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("Guilds = %+v, %v; want both guilds", all, err)
	}
}

func testChannels(t *testing.T, s store.Store) {
	ctx := context.Background()
	owner := user(1, "alice")
	if err := s.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	g := store.Guild{ID: id(10), Name: "g", OwnerID: owner.ID, CreatedAt: time.Now().UTC()}
	if err := s.CreateGuild(ctx, g); err != nil {
		t.Fatal(err)
	}
	// Insert out of order; listing must sort by position, then ID.
	for _, c := range []store.Channel{
		{ID: id(22), GuildID: g.ID, Name: "second", Position: 1, CreatedAt: time.Now().UTC()},
		{ID: id(20), GuildID: g.ID, Name: "first", Position: 0, CreatedAt: time.Now().UTC()},
	} {
		if err := s.CreateChannel(ctx, c); err != nil {
			t.Fatalf("CreateChannel: %v", err)
		}
	}
	got, err := s.ChannelsByGuild(ctx, g.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("ChannelsByGuild = %+v, %v", got, err)
	}
	if got[0].Name != "first" || got[1].Name != "second" {
		t.Fatalf("channels out of order: %s, %s", got[0].Name, got[1].Name)
	}
	if ch, err := s.ChannelByID(ctx, id(20)); err != nil || ch.Name != "first" {
		t.Fatalf("ChannelByID = %+v, %v", ch, err)
	}
	if _, err := s.ChannelByID(ctx, id(99)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing channel err = %v, want ErrNotFound", err)
	}
	if got, err := s.ChannelsByGuild(ctx, id(98)); err != nil || len(got) != 0 {
		t.Fatalf("channels of unknown guild = %+v, %v; want empty", got, err)
	}
}

func testMessagePaging(t *testing.T, s store.Store) {
	ctx := context.Background()
	author := user(1, "alice")
	if err := s.CreateUser(ctx, author); err != nil {
		t.Fatal(err)
	}
	g := store.Guild{ID: id(10), Name: "g", OwnerID: author.ID, CreatedAt: time.Now().UTC()}
	if err := s.CreateGuild(ctx, g); err != nil {
		t.Fatal(err)
	}
	ch := store.Channel{ID: id(20), GuildID: g.ID, Name: "general", CreatedAt: time.Now().UTC()}
	if err := s.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	const n = 5
	for i := range n {
		m := store.Message{
			ID: id(100 + i), ChannelID: ch.ID, Author: author,
			Content: fmt.Sprintf("msg%d", i), CreatedAt: time.Now().UTC(),
		}
		if err := s.CreateMessage(ctx, m); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	// Newest first (PROTOCOL §3).
	page, err := s.MessagesByChannel(ctx, ch.ID, "", 3)
	if err != nil || len(page) != 3 {
		t.Fatalf("first page = %d msgs, %v; want 3", len(page), err)
	}
	if page[0].Content != "msg4" || page[2].Content != "msg2" {
		t.Fatalf("first page order = %s..%s, want msg4..msg2", page[0].Content, page[2].Content)
	}
	if page[0].Author.Username != "alice" {
		t.Fatalf("author not hydrated: %+v", page[0].Author)
	}

	// `before` is exclusive, so paging never repeats or skips a message.
	next, err := s.MessagesByChannel(ctx, ch.ID, page[2].ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 2 || next[0].Content != "msg1" || next[1].Content != "msg0" {
		t.Fatalf("second page = %+v; want msg1, msg0", contents(next))
	}
	if empty, err := s.MessagesByChannel(ctx, ch.ID, next[1].ID, 3); err != nil || len(empty) != 0 {
		t.Fatalf("past the oldest = %v, %v; want empty", contents(empty), err)
	}
}

func contents(ms []store.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
}
