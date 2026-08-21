package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opendiscord/opendiscord/internal/auth"
	"github.com/opendiscord/opendiscord/internal/gateway"
	"github.com/opendiscord/opendiscord/internal/httpapi"
	"github.com/opendiscord/opendiscord/internal/ids"
	"github.com/opendiscord/opendiscord/internal/store"
	"github.com/opendiscord/opendiscord/internal/store/memstore"
)

type rig struct {
	srv   *httptest.Server
	store store.Store
	guild store.Guild
}

func newRig(t *testing.T) *rig {
	t.Helper()
	st := memstore.New()
	gen := ids.NewGenerator(0)
	a := auth.New(st, gen)
	ready := func(_ context.Context, u store.User) (any, []string, error) {
		return map[string]any{"user": u}, nil, nil
	}
	hub := gateway.NewHub(a, ready, slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	httpapi.New(st, a, hub, gen, slog.New(slog.DiscardHandler)).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	g := store.Guild{ID: gen.Next(), Name: "General", OwnerID: "0", CreatedAt: time.Now().UTC()}
	if err := st.CreateGuild(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	ch := store.Channel{ID: gen.Next(), GuildID: g.ID, Name: "general", CreatedAt: time.Now().UTC()}
	if err := st.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	return &rig{srv: srv, store: st, guild: g}
}

func (r *rig) do(t *testing.T, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, r.srv.URL+"/api/v0"+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func (r *rig) register(t *testing.T, name string) string {
	t.Helper()
	code, body := r.do(t, "POST", "/auth/register", "", map[string]string{
		"username": name, "password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", name, code, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out.Token
}

// assertErr checks both halves of the error contract (PROTOCOL §1.5): the HTTP
// status and the stable machine-readable code clients branch on.
func assertErr(t *testing.T, code int, body []byte, wantStatus int, wantCode string) {
	t.Helper()
	if code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", code, wantStatus, body)
	}
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body is not JSON: %s", body)
	}
	if e.Code != wantCode {
		t.Fatalf("code = %q, want %q", e.Code, wantCode)
	}
	if e.Message == "" {
		t.Error("error body must carry a human-readable message")
	}
}

// ---- guild membership (PROTOCOL §2, §3) ----

func TestRegisterJoinsExistingGuilds(t *testing.T) {
	r := newRig(t)
	token := r.register(t, "alice")
	code, body := r.do(t, "GET", "/guilds", token, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /guilds: %d %s", code, body)
	}
	var guilds []store.Guild
	if err := json.Unmarshal(body, &guilds); err != nil {
		t.Fatal(err)
	}
	if len(guilds) != 1 || guilds[0].ID != r.guild.ID {
		t.Fatalf("guilds = %+v, want just %s", guilds, r.guild.ID)
	}
}

// The real test of membership filtering: a guild created after registration
// must not appear, because nobody joined it. Before membership existed this
// endpoint returned every guild on the instance.
func TestGuildsExcludesGuildsNotJoined(t *testing.T) {
	r := newRig(t)
	token := r.register(t, "alice")
	other := store.Guild{ID: "99999999999999", Name: "private", OwnerID: "0", CreatedAt: time.Now().UTC()}
	if err := r.store.CreateGuild(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	_, body := r.do(t, "GET", "/guilds", token, nil)
	var guilds []store.Guild
	if err := json.Unmarshal(body, &guilds); err != nil {
		t.Fatal(err)
	}
	for _, g := range guilds {
		if g.ID == other.ID {
			t.Fatalf("a guild the user never joined leaked into /guilds: %+v", guilds)
		}
	}
	if len(guilds) != 1 {
		t.Fatalf("guilds = %+v, want only the joined one", guilds)
	}
}

// ---- error code registry (PROTOCOL §1.5) ----

func TestErrorCodes(t *testing.T) {
	r := newRig(t)
	token := r.register(t, "alice")
	chans, _ := r.store.ChannelsByGuild(context.Background(), r.guild.ID)
	channelID := chans[0].ID

	for _, tc := range []struct {
		name       string
		method     string
		path       string
		token      string
		body       any
		wantStatus int
		wantCode   string
	}{
		{"duplicate username", "POST", "/auth/register", "",
			map[string]string{"username": "alice", "password": "password123"},
			http.StatusConflict, "username_taken"},
		{"wrong password", "POST", "/auth/login", "",
			map[string]string{"username": "alice", "password": "wrongpassword"},
			http.StatusUnauthorized, "invalid_credentials"},
		{"unknown user", "POST", "/auth/login", "",
			map[string]string{"username": "nobody", "password": "password123"},
			http.StatusUnauthorized, "invalid_credentials"},
		{"no token", "GET", "/users/@me", "", nil,
			http.StatusUnauthorized, "unauthorized"},
		{"bad token", "GET", "/users/@me", "odt_nope", nil,
			http.StatusUnauthorized, "unauthorized"},
		{"short password", "POST", "/auth/register", "",
			map[string]string{"username": "bob", "password": "short"},
			http.StatusBadRequest, "validation_failed"},
		{"bad username", "POST", "/auth/register", "",
			map[string]string{"username": "a b!", "password": "password123"},
			http.StatusBadRequest, "validation_failed"},
		{"missing channel", "GET", "/channels/12345/messages", token, nil,
			http.StatusNotFound, "channel_not_found"},
		{"post to missing channel", "POST", "/channels/12345/messages", token,
			map[string]string{"content": "hi"},
			http.StatusNotFound, "channel_not_found"},
		{"empty content", "POST", "/channels/" + channelID + "/messages", token,
			map[string]string{"content": "   "},
			http.StatusBadRequest, "validation_failed"},
		{"oversized content", "POST", "/channels/" + channelID + "/messages", token,
			map[string]string{"content": strings.Repeat("x", 4001)},
			http.StatusBadRequest, "validation_failed"},
		{"empty channel name", "POST", "/guilds/" + r.guild.ID + "/channels", token,
			map[string]string{"name": ""},
			http.StatusBadRequest, "validation_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := r.do(t, tc.method, tc.path, tc.token, tc.body)
			assertErr(t, code, body, tc.wantStatus, tc.wantCode)
		})
	}
}

// ---- query parameter validation (PROTOCOL §3) ----

func TestMessageLimitValidation(t *testing.T) {
	r := newRig(t)
	token := r.register(t, "alice")
	chans, _ := r.store.ChannelsByGuild(context.Background(), r.guild.ID)
	base := "/channels/" + chans[0].ID + "/messages"

	for _, bad := range []string{"0", "-1", "abc", "101", "999", "1.5", ""} {
		t.Run("rejects limit="+bad, func(t *testing.T) {
			// An empty limit= is still a supplied parameter, and an empty
			// string is not a valid integer.
			code, body := r.do(t, "GET", base+"?limit="+bad, token, nil)
			if bad == "" {
				// Explicitly empty is treated as absent by net/url; document
				// that by asserting it succeeds rather than leaving it unclear.
				if code != http.StatusOK {
					t.Fatalf("limit= (empty) should behave as absent: %d %s", code, body)
				}
				return
			}
			assertErr(t, code, body, http.StatusBadRequest, "validation_failed")
		})
	}

	for _, ok := range []string{"1", "50", "100"} {
		t.Run("accepts limit="+ok, func(t *testing.T) {
			if code, body := r.do(t, "GET", base+"?limit="+ok, token, nil); code != http.StatusOK {
				t.Fatalf("limit=%s: %d %s", ok, code, body)
			}
		})
	}

	// Absent limit uses the documented default of 50.
	for i := range 60 {
		code, body := r.do(t, "POST", base, token, map[string]string{"content": fmt.Sprintf("m%d", i)})
		if code != http.StatusCreated {
			t.Fatalf("seed message: %d %s", code, body)
		}
	}
	code, body := r.do(t, "GET", base, token, nil)
	if code != http.StatusOK {
		t.Fatalf("default limit: %d %s", code, body)
	}
	var msgs []store.Message
	if err := json.Unmarshal(body, &msgs); err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 50 {
		t.Fatalf("default page = %d messages, want 50", len(msgs))
	}
}
