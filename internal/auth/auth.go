// Package auth: registration, login, opaque bearer tokens (PROTOCOL §1.4, §3).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/KinonNeko/openorder/internal/ids"
	"github.com/KinonNeko/openorder/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUsernameTaken      = errors.New("username taken")
	ErrBadUsername        = errors.New("username must be 2-32 chars: letters, digits, _ . -")
	ErrBadPassword        = errors.New("password must be 8-128 chars")
)

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{2,32}$`)

type Service struct {
	store store.Store
	ids   *ids.Generator
}

func New(s store.Store, gen *ids.Generator) *Service {
	return &Service{store: s, ids: gen}
}

func (s *Service) Register(ctx context.Context, username, password string) (store.User, string, error) {
	if !usernameRe.MatchString(username) {
		return store.User{}, "", ErrBadUsername
	}
	if len(password) < 8 || len(password) > 128 {
		return store.User{}, "", ErrBadPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return store.User{}, "", err
	}
	u := store.User{
		ID:          s.ids.Next(),
		Username:    username,
		DisplayName: username,
		CreatedAt:   time.Now().UTC(),
		PassHash:    hash,
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return store.User{}, "", ErrUsernameTaken
		}
		return store.User{}, "", err
	}
	token, err := s.issueToken(ctx, u.ID)
	return u, token, err
}

func (s *Service) Login(ctx context.Context, username, password string) (store.User, string, error) {
	u, err := s.store.UserByName(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.User{}, "", ErrInvalidCredentials
		}
		return store.User{}, "", err
	}
	if bcrypt.CompareHashAndPassword(u.PassHash, []byte(password)) != nil {
		return store.User{}, "", ErrInvalidCredentials
	}
	token, err := s.issueToken(ctx, u.ID)
	return u, token, err
}

// Authenticate resolves a bearer token to its user.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, error) {
	if token == "" {
		return store.User{}, ErrInvalidCredentials
	}
	uid, err := s.store.UserIDByToken(ctx, token)
	if err != nil {
		return store.User{}, ErrInvalidCredentials
	}
	return s.store.UserByID(ctx, uid)
}

func (s *Service) issueToken(ctx context.Context, userID string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := "oot_" + hex.EncodeToString(buf)
	return token, s.store.CreateToken(ctx, token, userID)
}
