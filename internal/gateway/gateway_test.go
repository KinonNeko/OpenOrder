package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/opendiscord/opendiscord/internal/auth"
	"github.com/opendiscord/opendiscord/internal/ids"
	"github.com/opendiscord/opendiscord/internal/store"
	"github.com/opendiscord/opendiscord/internal/store/memstore"
)

// guildsOf lets a test say which guilds a given user belongs to, which is what
// the hub filters fan-out against (PROTOCOL §4.5).
type guildsOf map[string][]string

type harness struct {
	hub  *Hub
	auth *auth.Service
	url  string
}

func newHarness(t *testing.T, membership guildsOf) *harness {
	t.Helper()
	st := memstore.New()
	a := auth.New(st, ids.NewGenerator(0))
	ready := func(_ context.Context, u store.User) (any, []string, error) {
		g, ok := membership[u.Username]
		if !ok {
			g = []string{"g1"}
		}
		return map[string]any{"user": u, "guilds": g}, g, nil
	}
	h := NewHub(a, ready, slog.New(slog.DiscardHandler))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &harness{hub: h, auth: a, url: "ws" + strings.TrimPrefix(srv.URL, "http")}
}

func (h *harness) token(t *testing.T, name string) string {
	t.Helper()
	_, tok, err := h.auth.Register(context.Background(), name, "password123")
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return tok
}

func (h *harness) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(h.url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) Frame {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var f Frame
	if err := conn.ReadJSON(&f); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return f
}

// connect performs the full HELLO -> IDENTIFY -> READY handshake and returns
// the READY frame.
func (h *harness) connect(t *testing.T, name string) (*websocket.Conn, Frame) {
	t.Helper()
	tok := h.token(t, name)
	conn := h.dial(t)
	if hello := readFrame(t, conn); hello.Op != OpHello {
		t.Fatalf("first frame op = %d, want HELLO(%d)", hello.Op, OpHello)
	}
	if err := conn.WriteJSON(Frame{Op: OpIdentify, D: json.RawMessage(`{"token":"` + tok + `"}`)}); err != nil {
		t.Fatalf("write IDENTIFY: %v", err)
	}
	ready := readFrame(t, conn)
	if ready.Op != OpDispatch || ready.T != "READY" {
		t.Fatalf("want READY dispatch, got op=%d t=%q", ready.Op, ready.T)
	}
	return conn, ready
}

// closeCode drains until the connection closes and reports the close code.
func closeCode(t *testing.T, conn *websocket.Conn) int {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			var ce *websocket.CloseError
			if ok := asCloseError(err, &ce); ok {
				return ce.Code
			}
			t.Fatalf("want a close error, got %v", err)
		}
	}
}

func asCloseError(err error, target **websocket.CloseError) bool {
	ce, ok := err.(*websocket.CloseError)
	if ok {
		*target = ce
	}
	return ok
}

// ---- sequence numbering (PROTOCOL §4.1) ----

func TestReadyIsSequenceOneAndDispatchesFollow(t *testing.T) {
	h := newHarness(t, nil)
	conn, ready := h.connect(t, "alice")
	if ready.S == nil || *ready.S != 1 {
		t.Fatalf("READY s = %v, want 1", ready.S)
	}
	for want := int64(2); want <= 4; want++ {
		h.hub.Dispatch("g1", "MESSAGE_CREATE", map[string]string{"content": "hi"})
		f := readFrame(t, conn)
		if f.S == nil || *f.S != want {
			t.Fatalf("dispatch s = %v, want %d", f.S, want)
		}
	}
}

// The whole reason `s` is per-connection: two clients must each see a gapless
// run starting at 1, so either can detect a dropped frame on its own.
func TestSequenceIsPerConnectionNotGlobal(t *testing.T) {
	h := newHarness(t, nil)
	a, readyA := h.connect(t, "alice")
	b, readyB := h.connect(t, "bob")
	if *readyA.S != 1 || *readyB.S != 1 {
		t.Fatalf("both READYs must be s=1, got %d and %d", *readyA.S, *readyB.S)
	}
	h.hub.Dispatch("g1", "MESSAGE_CREATE", map[string]string{"content": "hi"})
	fa, fb := readFrame(t, a), readFrame(t, b)
	if *fa.S != 2 || *fb.S != 2 {
		t.Fatalf("both must see s=2, got %d and %d", *fa.S, *fb.S)
	}
}

// ---- fan-out filtering (PROTOCOL §4.5) ----

func TestDispatchOnlyReachesGuildMembers(t *testing.T) {
	h := newHarness(t, guildsOf{"alice": {"g1"}, "bob": {"g2"}})
	a, _ := h.connect(t, "alice")
	b, _ := h.connect(t, "bob")

	h.hub.Dispatch("g1", "MESSAGE_CREATE", map[string]string{"content": "for g1"})
	h.hub.Dispatch("g2", "MESSAGE_CREATE", map[string]string{"content": "for g2"})

	// Asserting on each side's *first* frame is what makes this a real test:
	// if the filter were missing, bob's first frame would be the g1 event.
	if got := contentOf(t, readFrame(t, a)); got != "for g1" {
		t.Fatalf("alice first frame = %q, want the g1 event", got)
	}
	fb := readFrame(t, b)
	if got := contentOf(t, fb); got != "for g2" {
		t.Fatalf("bob first frame = %q, want the g2 event (he is not in g1)", got)
	}
	// The g1 event must not have consumed a sequence number on bob's
	// connection either -- filtered events are invisible, not silently skipped.
	if fb.S == nil || *fb.S != 2 {
		t.Fatalf("bob s = %v, want 2; a filtered event must not burn a sequence number", fb.S)
	}
}

func contentOf(t *testing.T, f Frame) string {
	t.Helper()
	var d struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(f.D, &d); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return d.Content
}

// ---- close codes (PROTOCOL §4.3) ----

func TestIdentifyTimeoutCloses4001(t *testing.T) {
	h := newHarness(t, nil)
	h.hub.identifyTimeout = 50 * time.Millisecond
	conn := h.dial(t)
	readFrame(t, conn) // HELLO
	if code := closeCode(t, conn); code != CloseIdentifyTimeout {
		t.Fatalf("close code = %d, want %d", code, CloseIdentifyTimeout)
	}
}

func TestWrongFirstOpCloses4002(t *testing.T) {
	h := newHarness(t, nil)
	conn := h.dial(t)
	readFrame(t, conn) // HELLO
	// A heartbeat before IDENTIFY is a client bug, not a timeout.
	if err := conn.WriteJSON(Frame{Op: OpHeartbeat}); err != nil {
		t.Fatal(err)
	}
	if code := closeCode(t, conn); code != CloseProtocolError {
		t.Fatalf("close code = %d, want %d", code, CloseProtocolError)
	}
}

func TestMalformedFrameCloses4002(t *testing.T) {
	h := newHarness(t, nil)
	conn := h.dial(t)
	readFrame(t, conn) // HELLO
	if err := conn.WriteMessage(websocket.TextMessage, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if code := closeCode(t, conn); code != CloseProtocolError {
		t.Fatalf("close code = %d, want %d", code, CloseProtocolError)
	}
}

func TestBadTokenCloses4004(t *testing.T) {
	h := newHarness(t, nil)
	conn := h.dial(t)
	readFrame(t, conn) // HELLO
	if err := conn.WriteJSON(Frame{Op: OpIdentify, D: json.RawMessage(`{"token":"odt_nope"}`)}); err != nil {
		t.Fatal(err)
	}
	if code := closeCode(t, conn); code != CloseAuthFailed {
		t.Fatalf("close code = %d, want %d", code, CloseAuthFailed)
	}
}

// ---- heartbeat ----

func TestHeartbeatIsAcknowledged(t *testing.T) {
	h := newHarness(t, nil)
	conn, _ := h.connect(t, "alice")
	// d carries the client's highest received `s` (PROTOCOL §4.2). v0 ignores
	// it, but sending it must not break the exchange.
	if err := conn.WriteJSON(Frame{Op: OpHeartbeat, D: json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	if f := readFrame(t, conn); f.Op != OpHeartbeatACK {
		t.Fatalf("op = %d, want HEARTBEAT_ACK(%d)", f.Op, OpHeartbeatACK)
	}
}

func TestSessionCountTracksLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	conn, _ := h.connect(t, "alice")
	if got := h.hub.Online(); got != 1 {
		t.Fatalf("online = %d, want 1", got)
	}
	conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for h.hub.Online() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := h.hub.Online(); got != 0 {
		t.Fatalf("online = %d after client hangup, want 0", got)
	}
}

// TestCloseLeavesSendChannelOpen pins the invariant behind a
// send-on-closed-channel panic that used to take down the whole process:
// close() closed s.send, while readLoop independently pushed heartbeat ACKs
// into that same channel, so any ACK racing the teardown panicked.
//
// The fix is that close() signals via done and never closes send. Asserting
// that directly is deterministic -- reintroduce close(s.send) and this test
// panics every run, where a concurrency flood would only catch it sometimes.
func TestCloseLeavesSendChannelOpen(t *testing.T) {
	h := newHarness(t, nil)
	conn, _ := h.connect(t, "alice")
	defer conn.Close()

	sess := h.onlySession(t)
	sess.close(websocket.CloseGoingAway, "test teardown")

	// A send here is exactly what readLoop may still be doing. On a closed
	// channel it panics; on an open one it either buffers or falls through.
	select {
	case sess.send <- []byte(`{"op":11}`):
	default:
	}

	// done must be closed, since that is how writeLoop learns to stop.
	select {
	case <-sess.done:
	default:
		t.Fatal("close() must close done so writeLoop can exit")
	}

	// close() is idempotent: the second call must not double-close done.
	sess.close(websocket.CloseGoingAway, "again")
}

func (h *harness) onlySession(t *testing.T) *session {
	t.Helper()
	h.hub.mu.RLock()
	defer h.hub.mu.RUnlock()
	if len(h.hub.sessions) != 1 {
		t.Fatalf("want exactly 1 session, got %d", len(h.hub.sessions))
	}
	for s := range h.hub.sessions {
		return s
	}
	return nil
}

// TestTeardownUnderLoadIsRaceFree exercises the same teardown through the real
// lifecycle -- clients flooding heartbeats while the hub tears sessions down --
// so -race can flag anything the targeted test above does not model.
func TestTeardownUnderLoadIsRaceFree(t *testing.T) {
	h := newHarness(t, nil)
	blob := map[string]string{"content": strings.Repeat("x", 8<<10)}
	for round := range 10 {
		conn, _ := h.connect(t, fmt.Sprintf("racer%d", round))
		sess := h.onlySession(t)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range 300 {
				if err := conn.WriteJSON(Frame{Op: OpHeartbeat}); err != nil {
					return
				}
			}
		}()
		for range 32 {
			h.hub.Dispatch("g1", "MESSAGE_CREATE", blob)
		}
		sess.close(websocket.CloseGoingAway, "load teardown")
		<-done
		conn.Close()
	}
}
