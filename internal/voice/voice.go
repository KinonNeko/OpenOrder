// Package voice maps OpenOrder voice channels onto LiveKit rooms and mints
// the join tokens clients present to the SFU (docs/PROTOCOL.md §5,
// docs/PLANNING.md §3.1 decision C).
//
// The authorization boundary is the point of this package: LiveKit never
// decides who may do what. This service resolves the caller's permissions
// against the main system, encodes that decision into a short-lived signed
// grant, and LiveKit does nothing but enforce what we already decided. That is
// why the token TTL is minutes, not hours -- it is a capability, not a session.
package voice

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/KinonNeko/openorder/internal/store"
)

// Room name prefix. Channel IDs are snowflakes and already globally unique;
// the prefix namespaces channel rooms against future non-channel rooms
// (DM calls, stage events) sharing one LiveKit deployment.
const roomPrefix = "ch_"

// DefaultTokenTTL bounds how long a mint stays usable. It only needs to cover
// the walk from "user clicked join" to "SFU handshake done".
const DefaultTokenTTL = 5 * time.Minute

var (
	ErrDisabled    = errors.New("voice: not configured")
	ErrNotVoice    = errors.New("voice: channel is not a voice channel")
	ErrNoGrants    = errors.New("voice: caller may not join this channel")
	ErrBadIdentity = errors.New("voice: user has no id")
)

// Config is the LiveKit deployment this instance federates voice to.
type Config struct {
	// URL is the address clients dial, e.g. "ws://127.0.0.1:7880". It is handed
	// to clients verbatim; it is not necessarily reachable from this process.
	URL string
	// APIKey / APISecret are the shared HS256 credentials. The secret never
	// leaves the server.
	APIKey    string
	APISecret string
	TokenTTL  time.Duration
}

type Service struct{ cfg Config }

// New returns a Service. A zero-value Config yields a disabled Service, which
// is the v0 default: the server must run fine with no LiveKit deployment.
func New(cfg Config) *Service {
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = DefaultTokenTTL
	}
	return &Service{cfg: cfg}
}

// Enabled reports whether voice is configured. Handlers return
// `voice_disabled` when it is not.
func (s *Service) Enabled() bool {
	return s.cfg.URL != "" && s.cfg.APIKey != "" && s.cfg.APISecret != ""
}

// URL is the LiveKit address to hand to clients.
func (s *Service) URL() string { return s.cfg.URL }

// RoomName maps a voice channel to its LiveKit room. Total and stable: the
// room is a projection of the channel, never separate state to reconcile.
func RoomName(channelID string) string { return roomPrefix + channelID }

// ChannelID reverses RoomName, for interpreting LiveKit webhooks.
func ChannelID(room string) (string, bool) { return strings.CutPrefix(room, roomPrefix) }

// Grants is this system's authorization decision about one user in one
// channel. M1's role system produces it; until then callers build it directly.
// It deliberately mirrors only the subset of LiveKit's grant surface we are
// willing to expose -- an unlisted capability is one we have not yet decided
// the permission semantics for.
type Grants struct {
	// Publish: may transmit microphone, camera and screen share.
	Publish bool `json:"publish"`
	// Subscribe: may receive other participants' tracks.
	Subscribe bool `json:"subscribe"`
	// PublishData: may use the LiveKit data channel.
	PublishData bool `json:"publish_data"`
	// Hidden: present in the room but invisible to other participants
	// (moderation observation). Not exposed in v0.
	Hidden bool `json:"-"`
}

// Speaker is the ordinary member grant.
func Speaker() Grants { return Grants{Publish: true, Subscribe: true, PublishData: true} }

// Listener may hear but not transmit (M1: the "mute" moderation state, and
// the audience role of a future stage channel).
func Listener() Grants { return Grants{Subscribe: true, PublishData: false} }

func (g Grants) any() bool { return g.Publish || g.Subscribe || g.PublishData }

// IssueToken mints a LiveKit join token for u in channelID under g.
//
// identity is the user's snowflake, never the username: usernames are mutable
// and LiveKit keys participants by identity. It also gives us Discord's
// semantics for free -- LiveKit admits one participant per identity per room,
// so the same account cannot occupy one voice channel twice.
func (s *Service) IssueToken(u store.User, channelID string, g Grants) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	if u.ID == "" {
		return "", ErrBadIdentity
	}
	if !g.any() {
		return "", ErrNoGrants
	}
	now := time.Now()
	claims := lkClaims{
		Iss:  s.cfg.APIKey,
		Sub:  u.ID,
		Jti:  u.ID,
		Nbf:  now.Add(-10 * time.Second).Unix(), // absorb small clock skew
		Exp:  now.Add(s.cfg.TokenTTL).Unix(),
		Name: firstNonEmpty(u.DisplayName, u.Username),
		Video: lkVideoGrant{
			Room:           RoomName(channelID),
			RoomJoin:       true,
			CanPublish:     &g.Publish,
			CanSubscribe:   &g.Subscribe,
			CanPublishData: &g.PublishData,
			Hidden:         &g.Hidden,
		},
	}
	return signHS256(claims, s.cfg.APISecret)
}

// IssueAdminToken mints a server-side management token for the LiveKit REST
// (Twirp) API: room listing, participant removal, forced mute -- the transport
// for the moderation actions in PLANNING §2.1 ("服务器静音/闭麦、拖拽移动成员").
//
// This token is never handed to a client. It is the credential this process
// uses to talk to the SFU, so it is minted per call and lives seconds.
func (s *Service) IssueAdminToken(room string) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	now := time.Now()
	return signHS256(lkClaims{
		Iss: s.cfg.APIKey,
		Sub: "openorder-server",
		Nbf: now.Add(-10 * time.Second).Unix(),
		Exp: now.Add(30 * time.Second).Unix(),
		Video: lkVideoGrant{
			Room:       room,
			RoomAdmin:  true,
			RoomList:   true,
			RoomCreate: true,
		},
	}, s.cfg.APISecret)
}

// ---- LiveKit token wire format ----
//
// A LiveKit access token is a plain HS256 JWT; the grant lives in a custom
// "video" claim. We sign it with stdlib rather than depending on
// github.com/livekit/protocol, which drags grpc, protobuf, prometheus, otel
// and zap into the monolith for what is ~40 lines of HMAC (PLANNING §2.3:
// dependency convergence). voice_conformance_test.go pins the claim shape
// against the real server, which is what makes this safe to hand-roll.

type lkVideoGrant struct {
	Room           string `json:"room,omitempty"`
	RoomJoin       bool   `json:"roomJoin,omitempty"`
	CanPublish     *bool  `json:"canPublish,omitempty"`
	CanSubscribe   *bool  `json:"canSubscribe,omitempty"`
	CanPublishData *bool  `json:"canPublishData,omitempty"`
	Hidden         *bool  `json:"hidden,omitempty"`
	RoomAdmin      bool   `json:"roomAdmin,omitempty"`
	RoomList       bool   `json:"roomList,omitempty"`
	RoomCreate     bool   `json:"roomCreate,omitempty"`
}

type lkClaims struct {
	Iss   string       `json:"iss"`
	Sub   string       `json:"sub"`
	Jti   string       `json:"jti,omitempty"`
	Nbf   int64        `json:"nbf"`
	Exp   int64        `json:"exp"`
	Name  string       `json:"name,omitempty"`
	Video lkVideoGrant `json:"video"`
}

func signHS256(claims any, secret string) (string, error) {
	// Header is constant, so it is spelled out rather than marshalled.
	const header = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := header + "." + b64(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	return signing + "." + b64(mac.Sum(nil)), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
