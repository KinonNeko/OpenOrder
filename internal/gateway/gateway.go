// Package gateway implements the WebSocket event stream (PROTOCOL §4):
// HELLO → IDENTIFY → READY, heartbeats, and fan-out of dispatch events.
//
// v0 fan-out is in-process (single node). Redis pub/sub takes over here when
// the gateway becomes independently scalable (PLANNING.md §3.1 D).
package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
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

// ReadyBuilder assembles the READY payload for a just-identified user.
type ReadyBuilder func(ctx context.Context, u store.User) (any, error)

type session struct {
	conn *websocket.Conn
	user store.User
	send chan []byte
	hub  *Hub
	once sync.Once
}

type Hub struct {
	auth     *auth.Service
	ready    ReadyBuilder
	log      *slog.Logger
	upgrader websocket.Upgrader

	mu       sync.RWMutex
	sessions map[*session]struct{}
	seq      int64
}

func NewHub(a *auth.Service, ready ReadyBuilder, log *slog.Logger) *Hub {
	return &Hub{
		auth:  a,
		ready: ready,
		log:   log,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// v0: the reference client may be served from another origin in dev.
			CheckOrigin: func(*http.Request) bool { return true },
		},
		sessions: map[*session]struct{}{},
	}
}

// Dispatch broadcasts an event to every authenticated session (PROTOCOL §4.5).
func (h *Hub) Dispatch(event string, payload any) {
	d, err := json.Marshal(payload)
	if err != nil {
		h.log.Error("gateway: marshal dispatch", "event", event, "err", err)
		return
	}
	h.mu.Lock()
	h.seq++
	s := h.seq
	frame, _ := json.Marshal(Frame{Op: OpDispatch, T: event, S: &s, D: d})
	for sess := range h.sessions {
		select {
		case sess.send <- frame:
		default:
			// Slow consumer: drop the session rather than block the hub.
			go sess.close(websocket.CloseGoingAway, "send buffer overflow")
		}
	}
	h.mu.Unlock()
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
	sess := &session{conn: conn, send: make(chan []byte, sendBuffer), hub: h}

	hello, _ := json.Marshal(map[string]any{"heartbeat_interval_ms": heartbeatInterval.Milliseconds()})
	if err := sess.writeFrame(Frame{Op: OpHello, D: hello}); err != nil {
		conn.Close()
		return
	}

	// Await IDENTIFY.
	conn.SetReadDeadline(time.Now().Add(identifyTimeout))
	var f Frame
	if err := conn.ReadJSON(&f); err != nil || f.Op != OpIdentify {
		sess.close(CloseIdentifyTimeout, "expected IDENTIFY")
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

	readyPayload, err := h.ready(r.Context(), user)
	if err != nil {
		sess.close(websocket.CloseInternalServerErr, "ready failed")
		return
	}
	d, _ := json.Marshal(readyPayload)
	h.mu.Lock()
	h.seq++
	s := h.seq
	h.sessions[sess] = struct{}{}
	h.mu.Unlock()
	if err := sess.writeFrame(Frame{Op: OpDispatch, T: "READY", S: &s, D: d}); err != nil {
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
	for b := range s.send {
		s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := s.conn.WriteMessage(websocket.TextMessage, b); err != nil {
			s.close(websocket.CloseGoingAway, "write failed")
			return
		}
	}
}

func (s *session) readLoop() {
	// Allow two missed heartbeats (PROTOCOL §4.3).
	deadline := func() { s.conn.SetReadDeadline(time.Now().Add(2*heartbeatInterval + 5*time.Second)) }
	deadline()
	for {
		var f Frame
		if err := s.conn.ReadJSON(&f); err != nil {
			s.close(CloseHeartbeatLost, "read failed or heartbeat lost")
			return
		}
		if f.Op == OpHeartbeat {
			deadline()
			ack, _ := json.Marshal(Frame{Op: OpHeartbeatACK})
			select {
			case s.send <- ack:
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
		msg := websocket.FormatCloseMessage(code, reason)
		s.conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(time.Second))
		s.conn.Close()
		close(s.send)
		if s.user.ID != "" {
			s.hub.log.Info("gateway: session closed", "user", s.user.Username, "online", s.hub.Online())
		}
	})
}
