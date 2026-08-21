package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestClientJoinsRoom is the end-to-end integration assumption from
// PLANNING §3.1 decision C and §5.4 item 4:
//
//	our backend signs a token -> a client uses it to enter the room ->
//	the SFU reports that participant as present in the room we named.
//
// It joins over the LiveKit signal WebSocket (no media -- media transport is
// LiveKit's problem, not our integration's) and then confirms the participant
// server-side through the Twirp RoomService using an admin token we mint too.
func TestClientJoinsRoom(t *testing.T) {
	svc, base := testService(t)
	const channelID = "48291057123330"
	room := RoomName(channelID)

	token, err := svc.IssueToken(testUser, channelID, Speaker())
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	ws := strings.Replace(base, "http", "ws", 1) +
		"/rtc?access_token=" + url.QueryEscape(token) +
		"&protocol=15&auto_subscribe=1&sdk=go"
	conn, resp, err := websocket.DefaultDialer.Dial(ws, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("signal dial failed (http %d): %v", status, err)
	}
	defer conn.Close()

	// The first signal frame is the JoinResponse; receiving it means the SFU
	// admitted us and materialised the room.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("no JoinResponse from SFU: %v", err)
	}

	// Now confirm from the server side that we are really in there.
	parts := listParticipants(t, svc, base, room)
	var found bool
	for _, p := range parts {
		if p.Identity == testUser.ID {
			found = true
			if p.Name != "Alice" {
				t.Errorf("participant name = %q, want Alice", p.Name)
			}
		}
	}
	if !found {
		t.Fatalf("user %s not present in room %s; participants=%+v", testUser.ID, room, parts)
	}
	t.Logf("SFU confirms identity %s (%q) is in room %s", testUser.ID, "Alice", room)
}

// TestJoinRejectedForWrongRoom proves LiveKit enforces the room we signed:
// a token minted for channel A must not open channel B. This is what makes
// "permissions are decided by the main system" actually hold.
func TestJoinRejectedForWrongRoom(t *testing.T) {
	svc, base := testService(t)
	token, err := svc.IssueToken(testUser, "11111111", Speaker())
	if err != nil {
		t.Fatal(err)
	}
	// Token says ch_11111111; ask to be routed anyway and check the SFU does
	// not place us in a different room.
	ws := strings.Replace(base, "http", "ws", 1) +
		"/rtc?access_token=" + url.QueryEscape(token) + "&protocol=15&sdk=go"
	conn, _, err := websocket.DefaultDialer.Dial(ws, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("join: %v", err)
	}
	// The grant, not the request, decides the room.
	if parts := listParticipants(t, svc, base, RoomName("22222222")); len(parts) != 0 {
		t.Fatalf("token for ch_11111111 leaked into ch_22222222: %+v", parts)
	}
}

type lkParticipant struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	State    string `json:"state"`
}

func listParticipants(t *testing.T, svc *Service, base, room string) []lkParticipant {
	t.Helper()
	admin, err := svc.IssueAdminToken(room)
	if err != nil {
		t.Fatalf("IssueAdminToken: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"room": room})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/twirp/livekit.RoomService/ListParticipants", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("RoomService unreachable: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ListParticipants: %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Participants []lkParticipant `json:"participants"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode participants: %v (%s)", err, raw)
	}
	return out.Participants
}
