// opendiscord: modular-monolith server entrypoint.
//
// Configuration (env):
//
//	OD_ADDR         listen address           (default ":8080")
//	OD_STORE        "memory" | "postgres"    (default "memory")
//	OD_POSTGRES_DSN DSN when OD_STORE=postgres
//	OD_NODE_ID      snowflake node id 0-1023 (default 0)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/opendiscord/opendiscord/internal/auth"
	"github.com/opendiscord/opendiscord/internal/gateway"
	"github.com/opendiscord/opendiscord/internal/httpapi"
	"github.com/opendiscord/opendiscord/internal/ids"
	"github.com/opendiscord/opendiscord/internal/store"
	"github.com/opendiscord/opendiscord/internal/store/memstore"
	"github.com/opendiscord/opendiscord/internal/store/pgstore"
	"github.com/opendiscord/opendiscord/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	node, _ := strconv.ParseUint(env("OD_NODE_ID", "0"), 10, 64)
	gen := ids.NewGenerator(node)

	var st store.Store
	switch mode := env("OD_STORE", "memory"); mode {
	case "memory":
		st = memstore.New()
		log.Warn("using in-memory store; data is lost on restart")
	case "postgres":
		pg, err := pgstore.Open(ctx, os.Getenv("OD_POSTGRES_DSN"))
		if err != nil {
			log.Error("open postgres", "err", err)
			os.Exit(1)
		}
		defer pg.Close()
		st = pg
	default:
		log.Error("unknown OD_STORE", "value", mode)
		os.Exit(1)
	}

	if err := seedDefaultGuild(ctx, st, gen); err != nil {
		log.Error("seed default guild", "err", err)
		os.Exit(1)
	}

	authSvc := auth.New(st, gen)
	hub := gateway.NewHub(authSvc, readyBuilder(st), log)
	api := httpapi.New(st, authSvc, hub, gen, log)

	mux := http.NewServeMux()
	api.Routes(mux)
	mux.Handle("/", web.Handler())

	addr := env("OD_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Info("opendiscord listening", "addr", addr, "store", env("OD_STORE", "memory"))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

// seedDefaultGuild creates the v0 single default guild with a #general
// channel on first start (PROTOCOL §2, Guild).
func seedDefaultGuild(ctx context.Context, st store.Store, gen *ids.Generator) error {
	guilds, err := st.Guilds(ctx)
	if err != nil {
		return err
	}
	if len(guilds) > 0 {
		return nil
	}
	now := time.Now().UTC()
	g := store.Guild{ID: gen.Next(), Name: "General", OwnerID: "0", CreatedAt: now}
	if err := st.CreateGuild(ctx, g); err != nil {
		return err
	}
	return st.CreateChannel(ctx, store.Channel{
		ID: gen.Next(), GuildID: g.ID, Type: store.ChannelText,
		Name: "general", CreatedAt: now,
	})
}

// readyBuilder assembles the READY payload -- user + the guilds this user
// belongs to, with embedded channels (PROTOCOL §4.4) -- and returns those
// guild IDs for the hub to filter fan-out with (PROTOCOL §4.5).
func readyBuilder(st store.Store) gateway.ReadyBuilder {
	type guildWithChannels struct {
		store.Guild
		Channels []store.Channel `json:"channels"`
	}
	return func(ctx context.Context, u store.User) (any, []string, error) {
		guilds, err := st.GuildsByUser(ctx, u.ID)
		if err != nil {
			return nil, nil, err
		}
		out := make([]guildWithChannels, 0, len(guilds))
		ids := make([]string, 0, len(guilds))
		for _, g := range guilds {
			chans, err := st.ChannelsByGuild(ctx, g.ID)
			if err != nil {
				return nil, nil, err
			}
			if chans == nil {
				chans = []store.Channel{}
			}
			out = append(out, guildWithChannels{Guild: g, Channels: chans})
			ids = append(ids, g.ID)
		}
		return map[string]any{"user": u, "guilds": out}, ids, nil
	}
}
