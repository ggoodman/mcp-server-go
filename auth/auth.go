package auth

import (
	"context"
	"errors"
)

// ErrUnauthorized indicates authentication failed or no valid credentials were supplied.
var ErrUnauthorized = errors.New("unauthorized")

// ErrInsufficientScope indicates the caller authenticated but lacks required scope.
var ErrInsufficientScope = errors.New("insufficient scope")

// UserInfo represents an authenticated principal.
// Implementations should be lightweight and safe for concurrent use.
type UserInfo interface {
	// UserID returns the unique identifier for the user.
	UserID() string
	// Claims unmarshalls the user's claims into the provided struct reference.
	Claims(ref any) error
}

// Authenticator validates bearer tokens and returns associated user info.
// It should return ErrUnauthorized for invalid credentials.
type Authenticator interface {
	CheckAuthentication(ctx context.Context, tok string) (UserInfo, error)
}
