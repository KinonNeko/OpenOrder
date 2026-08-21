// Package gateway implements the WebSocket event stream (PROTOCOL §4):
// HELLO → IDENTIFY → READY, heartbeats, and fan-out of dispatch events.
//
// v0 fan-out is in-process (single node). Redis pub/sub takes over here when
// the gateway becomes independently scalable (PLANNING.md §3.1 D).
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/opendiscord/opendiscord/internal/auth"
	"github.com/opendiscord/opendiscord/internal/store"
)

// Opcodes (PROTOCOL §4.2).
const (
	OpDispatch     = 0
	OpHeartbeat    = 1
	OpIdentify     = 2
	OpHello        = 10
	OpHeartbeatACK = 11
)

// Close codes (PROTOCOL §4.3).
const (
	CloseIdentifyTimeout = 4001
	CloseProtocolError   = 4002
	CloseAuthFailed      = 4004
	CloseHeartbeatLost   = 4009
)

const (
	heartbeatInterval = 30 * time.Second
	identifyTimeout   = 10 * time.Second
	writeTimeout      = 10 * time.Second
	sendBuffer        = 64
)

type Frame struct {
	Op int             `json:"op"`
	T  string          `json:"t,omitempty"`
	S  *int64          `json:"s,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

// ReadyBuilder assembles the READY payload for a just-identified user and
// reports which guilds that user belongs to. The guild set is what the hub
// filters fan-out against (PROTOCOL §4.5), so it must come from the same
// source as the payload -- deriving it twice invites the two to disagree.
type ReadyBuilder func(ctx context.Context, u store.User) (payload any, guildIDs []string, err error)

type session struct {
	conn   *websocket.Conn
	user   store.User
	guilds map[string]struct{}
	send   chan []byte
	// done is closed exactly once, by close(). The send channel is deliberately
	// never closed: readLoop can be putting a heartbeat ACK into it at the
	// moment another goroutine tears the session down, and closing it there
	// would panic the whole process.
	done chan struct{}
	hub  *Hub
	// seq is this connection's own dispatch counter (PROTOCOL §4.1).
	// Guarded by hub.mu, except while the session is still unpublished during
	// the handshake, where it is confined to the accepting goroutine.
	seq  int64
	once sync.Once
}

type Hub struct {
	auth     *auth.Service
	ready    ReadyBuilder
	log      *slog.Logger
	upgrader websocket.Upgrader

	// identifyTimeout and heartbeatInterval are fields, not the constants
	// directly, so tests can compress the lifecycle into milliseconds.
	identifyTimeout   time.Duration
	heartbeatInterval time.Duration

	mu       sync.RWMutex
	sessions map[*session]struct{}
}

func NewHub(a *auth.Service, ready ReadyBuilder, log *slog.Logger) *Hub {
	return &Hub{
		auth:              a,
		ready:             ready,
		log:               log,
		identifyTimeout:   identifyTimeout,
		heartbeatInterval: heartbeatInterval,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// v0: the reference client may be served from another origin in dev.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		sessions: map[*session]struct{}{},
	}
}

// Dispatch broadcasts an event to every authenticated session whose user is a
// member of guildID (PROTOCOL §4.5).
//
// The payload is marshalled once but the frame is built per session, because
// `s` is per-connection (PROTOCOL §4.1) -- a global counter would show every
// client gaps it cannot distinguish from lost events.
func (h *Hub) Dispatch(guildID, event string, payload any) {
	d, err := json.Marshal(payload)
	if err != nil {
		h.log.Error("gateway: marshal dispatch", "event", event, "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sess := range h.sessions {
		if _, ok := sess.guilds[guildID]; !ok {
			continue
		}
		sess.seq++
		n := sess.seq
		frame, err := json.Marshal(Frame{Op: OpDispatch, T: event, S: &n, D: d})
		if err != nil {
			h.log.Error("gateway: marshal frame", "event", event, "err", err)
			continue
		}
		select {
		case sess.send <- frame:
		default:
			// Slow consumer: drop the session rather than block the hub.
			go sess.close(websocket.CloseGoingAway, "send buffer overflow")
		}
	}
}

func (h *Hub) Online() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

// ServeHTTP upgrades the connection and runs the session lifecycle.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	sess := &session{
		conn: conn,
		send: make(chan []byte, sendBuffer),
		done: make(chan struct{}),
		hub:  h,
	}

	hello, _ := json.Marshal(map[string]any{"heartbeat_interval_ms": h.heartbeatInterval.Milliseconds()})
	if err := sess.writeFrame(Frame{Op: OpHello, D: hello}); err != nil {
		conn.Close()
		return
	}

	// Await IDENTIFY. 4001 means "you never sent it", 4002 means "you sent
	// something else" -- the client's response differs (PROTOCOL §4.3).
	conn.SetReadDeadline(time.Now().Add(h.identifyTimeout))
	var f Frame
	if err := conn.ReadJSON(&f); err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			sess.close(CloseIdentifyTimeout, "no IDENTIFY within timeout")
		} else {
			sess.close(CloseProtocolError, "malformed frame")
		}
		return
	}
	if f.Op != OpIdentify {
		sess.close(CloseProtocolError, "expected IDENTIFY")
		return
	}
	var ident struct {
		Token string `json:"token"`
	}
	json.Unmarshal(f.D, &ident)
	user, err := h.auth.Authenticate(r.Context(), ident.Token)
	if err != nil {
		sess.close(CloseAuthFailed, "authentication failed")
		return
	}
	sess.user = user

	readyPayload, guildIDs, err := h.ready(r.Context(), user)
	if err != nil {
		sess.close(websocket.CloseInternalServerErr, "ready failed")
		return
	}
	sess.guilds = make(map[string]struct{}, len(guildIDs))
	for _, id := range guildIDs {
		sess.guilds[id] = struct{}{}
	}
	d, _ := json.Marshal(readyPayload)
	// READY is frame 1 of this connection (PROTOCOL §4.1). Numbering it before
	// publishing the session keeps `s` gapless: no dispatch can slip in first.
	sess.seq = 1
	n := sess.seq
	h.mu.Lock()
	h.sessions[sess] = struct{}{}
	h.mu.Unlock()
	if err := sess.writeFrame(Frame{Op: OpDispatch, T: "READY", S: &n, D: d}); err != nil {
		sess.close(websocket.CloseGoingAway, "write failed")
		return
	}
	h.log.Info("gateway: session open", "user", user.Username, "online", h.Online())

	go sess.writeLoop()
	sess.readLoop()
}

func (s *session) writeFrame(f Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return s.conn.WriteMessage(websocket.TextMessage, b)
}

func (s *session) writeLoop() {
	for {
		select {
		case <-s.done:
			return
		case b := <-s.send:
			s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := s.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				s.close(websocket.CloseGoingAway, "write failed")
				return
			}
		}
	}
}

func (s *session) readLoop() {
	// Allow two missed heartbeats (PROTOCOL §4.3).
	hb := s.hub.heartbeatInterval
	deadline := func() { s.conn.SetReadDeadline(time.Now().Add(2*hb + 5*time.Second)) }
	deadline()
	for {
		var f Frame
		if err := s.conn.ReadJSON(&f); err != nil {
			s.close(CloseHeartbeatLost, "read failed or heartbeat lost")
			return
		}
		if f.Op == OpHeartbeat {
			// f.D carries the client's highest received `s`. v0 accepts but
			// does not use it; it is the replay cursor RESUME will need
			// (PROTOCOL §4.2).
			deadline()
			ack, _ := json.Marshal(Frame{Op: OpHeartbeatACK})
			select {
			case s.send <- ack:
			case <-s.done:
				return
			default:
			}
		}
		// All writes go through REST (PROTOCOL §3); other client ops are ignored in v0.
	}
}

func (s *session) close(code int, reason string) {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.sessions, s)
		s.hub.mu.Unlock()
		close(s.done)
		msg := websocket.FormatCloseMessage(code, reason)
		s.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		s.conn.Close()
		if s.user.ID != "" {
			s.hub.log.Info("gateway: session closed", "user", s.user.Username, "online", s.hub.Online())
		}
	})
}
