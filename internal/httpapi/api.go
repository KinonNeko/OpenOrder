// Package httpapi implements the REST surface (PROTOCOL §3) on net/http.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KinonNeko/openorder/internal/auth"
	"github.com/KinonNeko/openorder/internal/gateway"
	"github.com/KinonNeko/openorder/internal/ids"
	"github.com/KinonNeko/openorder/internal/store"
)

const maxMessageLen = 4000

type API struct {
	store store.Store
	auth  *auth.Service
	hub   *gateway.Hub
	ids   *ids.Generator
	log   *slog.Logger
}

func New(s store.Store, a *auth.Service, hub *gateway.Hub, gen *ids.Generator, log *slog.Logger) *API {
	return &API{store: s, auth: a, hub: hub, ids: gen, log: log}
}

// Routes mounts every /api/v0 handler plus the gateway upgrade endpoint.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v0/auth/register", a.handleRegister)
	mux.HandleFunc("POST /api/v0/auth/login", a.handleLogin)
	mux.HandleFunc("GET /api/v0/users/@me", a.authed(a.handleMe))
	mux.HandleFunc("GET /api/v0/gateway", a.handleGatewayURL)
	mux.HandleFunc("GET /api/v0/guilds", a.authed(a.handleGuilds))
	mux.HandleFunc("GET /api/v0/guilds/{guild_id}/channels", a.authed(a.handleChannels))
	mux.HandleFunc("POST /api/v0/guilds/{guild_id}/channels", a.authed(a.handleCreateChannel))
	mux.HandleFunc("GET /api/v0/channels/{channel_id}/messages", a.authed(a.handleMessages))
	mux.HandleFunc("POST /api/v0/channels/{channel_id}/messages", a.authed(a.handleCreateMessage))
	mux.Handle("GET /gateway", a.hub)
}

// ---- plumbing ----

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Code: code, Message: msg})
}

func (a *API) internal(w http.ResponseWriter, where string, err error) {
	a.log.Error("api: internal error", "where", where, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "validation_failed", "invalid JSON body")
		return false
	}
	return true
}

type userKey struct{}

func (a *API) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		u, err := a.auth.Authenticate(r.Context(), token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}

func currentUser(r *http.Request) store.User {
	u, _ := r.Context().Value(userKey{}).(store.User)
	return u
}

// ---- handlers ----

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	User  store.User `json:"user"`
	Token string     `json:"token"`
}

func (a *API) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, token, err := a.auth.Register(r.Context(), c.Username, c.Password)
	switch {
	case errors.Is(err, auth.ErrUsernameTaken):
		writeErr(w, http.StatusConflict, "username_taken", err.Error())
	case errors.Is(err, auth.ErrBadUsername), errors.Is(err, auth.ErrBadPassword):
		writeErr(w, http.StatusBadRequest, "validation_failed", err.Error())
	case err != nil:
		a.internal(w, "register", err)
	default:
		if err := a.joinAllGuilds(r.Context(), u.ID); err != nil {
			a.internal(w, "register_join", err)
			return
		}
		writeJSON(w, http.StatusCreated, authResponse{User: u, Token: token})
	}
}

// joinAllGuilds implements the v0 rule that registration joins every guild
// that exists at the time (PROTOCOL §2). It lives here rather than in
// auth.Service because membership is guild state, not credential state; M1
// replaces it with invites.
func (a *API) joinAllGuilds(ctx context.Context, userID string) error {
	guilds, err := a.store.Guilds(ctx)
	if err != nil {
		return err
	}
	for _, g := range guilds {
		if err := a.store.AddGuildMember(ctx, g.ID, userID); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	u, token, err := a.auth.Login(r.Context(), c.Username, c.Password)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeErr(w, http.StatusUnauthorized, "invalid_credentials", "wrong username or password")
	case err != nil:
		a.internal(w, "login", err)
	default:
		writeJSON(w, http.StatusOK, authResponse{User: u, Token: token})
	}
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func (a *API) handleGatewayURL(w http.ResponseWriter, r *http.Request) {
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": scheme + "://" + r.Host + "/gateway"})
}

func (a *API) handleGuilds(w http.ResponseWriter, r *http.Request) {
	guilds, err := a.store.GuildsByUser(r.Context(), currentUser(r).ID)
	if err != nil {
		a.internal(w, "guilds", err)
		return
	}
	if guilds == nil {
		guilds = []store.Guild{}
	}
	writeJSON(w, http.StatusOK, guilds)
}

func (a *API) handleChannels(w http.ResponseWriter, r *http.Request) {
	chans, err := a.store.ChannelsByGuild(r.Context(), r.PathValue("guild_id"))
	if err != nil {
		a.internal(w, "channels", err)
		return
	}
	if chans == nil {
		chans = []store.Channel{}
	}
	writeJSON(w, http.StatusOK, chans)
}

func (a *API) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))
	if name == "" || len(name) > 100 {
		writeErr(w, http.StatusBadRequest, "validation_failed", "channel name must be 1-100 chars")
		return
	}
	guildID := r.PathValue("guild_id")
	existing, err := a.store.ChannelsByGuild(r.Context(), guildID)
	if err != nil {
		a.internal(w, "create_channel", err)
		return
	}
	ch := store.Channel{
		ID:        a.ids.Next(),
		GuildID:   guildID,
		Type:      store.ChannelText,
		Name:      name,
		Position:  len(existing),
		CreatedAt: time.Now().UTC(),
	}
	if err := a.store.CreateChannel(r.Context(), ch); err != nil {
		a.internal(w, "create_channel", err)
		return
	}
	a.hub.Dispatch(ch.GuildID, "CHANNEL_CREATE", ch)
	writeJSON(w, http.StatusCreated, ch)
}

func (a *API) handleMessages(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channel_id")
	if _, err := a.store.ChannelByID(r.Context(), channelID); err != nil {
		writeErr(w, http.StatusNotFound, "channel_not_found", "channel does not exist")
		return
	}
	// An out-of-range limit is an error, not a silent fallback (PROTOCOL §3):
	// falling back turns a paging bug into "a few messages went missing".
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			writeErr(w, http.StatusBadRequest, "validation_failed", "limit must be an integer in 1-100")
			return
		}
		limit = n
	}
	msgs, err := a.store.MessagesByChannel(r.Context(), channelID, r.URL.Query().Get("before"), limit)
	if err != nil {
		a.internal(w, "messages", err)
		return
	}
	if msgs == nil {
		msgs = []store.Message{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (a *API) handleCreateMessage(w http.ResponseWriter, r *http.Request) {
	channelID := r.PathValue("channel_id")
	ch, err := a.store.ChannelByID(r.Context(), channelID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "channel_not_found", "channel does not exist")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &body) {
		return
	}
	content := strings.TrimSpace(body.Content)
	if content == "" || len(content) > maxMessageLen {
		writeErr(w, http.StatusBadRequest, "validation_failed", "content must be 1-4000 chars")
		return
	}
	msg := store.Message{
		ID:        a.ids.Next(),
		ChannelID: channelID,
		Author:    currentUser(r),
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}
	if err := a.store.CreateMessage(r.Context(), msg); err != nil {
		a.internal(w, "create_message", err)
		return
	}
	a.hub.Dispatch(ch.GuildID, "MESSAGE_CREATE", msg)
	writeJSON(w, http.StatusCreated, msg)
}
