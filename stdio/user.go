package stdio

import (
	"os/user"
)

// UserProvider provides a string user ID to associate with the stdio peer.
// For stdio, we don't pass or validate bearer tokens; this mirrors the library's
// examples where local development often uses a fixed or environment-derived ID.
type UserProvider interface {
	CurrentUserID() (string, error)
}

// OSUserProvider resolves the user ID using the operating system's current user.
// The returned ID is user.Username when available; falling back to user.Uid.
type OSUserProvider struct{}

func (OSUserProvider) CurrentUserID() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	if u.Username != "" {
		return u.Username, nil
	}
	return u.Uid, nil
}
