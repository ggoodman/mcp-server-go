package auth

import "context"

type UserInfo interface {
	// UserID returns the unique identifier for the user.
	UserID() string
	// Claims unmarshalls the user's claims into the provided struct reference.
	Claims(ref any) error
}

type Authenticator interface {
	CheckAuthentication(ctx context.Context, tok string) (UserInfo, error)
}
