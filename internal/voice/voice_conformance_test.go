package voice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opendiscord/opendiscord/internal/store"
)

// These tests pin our hand-rolled LiveKit token against a real livekit-server.
// They are the guard that lets internal/voice stay free of
// github.com/livekit/protocol (see the wire-format note in voice.go).
//
// Run against a local dev server:
//
//	livekit-server --dev --bind 127.0.0.1 &
//	OD_TEST_LIVEKIT_URL=http://127.0.0.1:7880 \
//	OD_TEST_LIVEKIT_KEY=devkey OD_TEST_LIVEKIT_SECRET=secret go test ./internal/voice
//
// Without those vars the conformance test skips, so `go test ./...` stays green
// on a machine with no SFU.

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	base := os.Getenv("OD_TEST_LIVEKIT_URL")
	if base == "" {
		t.Skip("OD_TEST_LIVEKIT_URL not set; skipping LiveKit conformance test")
	}
	return New(Config{
		URL:       base,
		APIKey:    envOr("OD_TEST_LIVEKIT_KEY", "devkey"),
		APISecret: envOr("OD_TEST_LIVEKIT_SECRET", "secret"),
	}), base
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var testUser = store.User{ID: "48291057123328", Username: "alice", DisplayName: "Alice"}

// validate asks livekit-server whether it would admit this token. A 200 means
// the SFU parsed our JWT, accepted the signature, and honoured the grant --
// exactly the integration assumption in PLANNING §3.1 decision C.
func validate(t *testing.T, base, token string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u := base + "/rtc/validate?access_token=" + url.QueryEscape(token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("livekit unreachable at %s: %v", base, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

func TestLiveKitAcceptsOurToken(t *testing.T) {
	svc, base := testService(t)
	token, err := svc.IssueToken(testUser, "48291057123330", Speaker())
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	code, body := validate(t, base, token)
	if code != http.StatusOK {
		t.Fatalf("livekit rejected our token: %d %s", code, body)
	}
	t.Logf("livekit accepted token for room %s: %s", RoomName("48291057123330"), body)
}

func TestLiveKitRejectsWrongSecret(t *testing.T) {
	_, base := testService(t)
	bad := New(Config{URL: base, APIKey: envOr("OD_TEST_LIVEKIT_KEY", "devkey"), APISecret: "not-the-secret"})
	token, err := bad.IssueToken(testUser, "48291057123330", Speaker())
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if code, body := validate(t, base, token); code == http.StatusOK {
		t.Fatalf("livekit accepted a token signed with the wrong secret: %s", body)
	}
}

func TestLiveKitRejectsExpiredToken(t *testing.T) {
	svc, base := testService(t)
	svc.cfg.TokenTTL = -time.Minute // already expired
	token, err := svc.IssueToken(testUser, "48291057123330", Speaker())
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if code, body := validate(t, base, token); code == http.StatusOK {
		t.Fatalf("livekit accepted an expired token: %s", body)
	}
}

// TestClaimShape decodes what we sign, so a change to the payload is visible
// in review rather than only at runtime.
func TestClaimShape(t *testing.T) {
	svc := New(Config{URL: "ws://x", APIKey: "devkey", APISecret: "secret"})
	token, err := svc.IssueToken(testUser, "48291057123330", Listener())
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("want 3 JWT segments, got %d", len(parts))
	}
	var got map[string]any
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["iss"] != "devkey" || got["sub"] != testUser.ID || got["name"] != "Alice" {
		t.Errorf("identity claims wrong: %v", got)
	}
	video, _ := got["video"].(map[string]any)
	if video["room"] != "ch_48291057123330" || video["roomJoin"] != true {
		t.Errorf("video grant wrong: %v", video)
	}
	if video["canSubscribe"] != true {
		t.Errorf("listener should subscribe: %v", video)
	}
	if _, present := video["canPublish"]; !present {
		t.Error("canPublish must be explicit false, not omitted: LiveKit defaults it to true")
	}
	if video["canPublish"] != false {
		t.Errorf("listener must not publish: %v", video)
	}
}

func TestRoomNameRoundTrip(t *testing.T) {
	const id = "48291057123330"
	ch, ok := ChannelID(RoomName(id))
	if !ok || ch != id {
		t.Fatalf("round trip failed: %q %v", ch, ok)
	}
	if _, ok := ChannelID("lobby"); ok {
		t.Error("non-channel room should not decode to a channel id")
	}
}

func TestDisabledServiceRefuses(t *testing.T) {
	if _, err := New(Config{}).IssueToken(testUser, "1", Speaker()); err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}
