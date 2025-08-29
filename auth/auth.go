package auth

import "net/http"

type AuthenticationResult interface {
	// IsAuthenticated returns true if the user is authenticated.
	// When this returns true, UserInfo() will return a valid UserInfo object.
	// When this returns false, GetAuthenticationChallenge() may return a valid challenge.
	IsAuthenticated() bool
	UserInfo() (UserInfo, error)
	GetAuthenticationChallenge() *AuthenticationChallenge
}

type UserInfo interface {
	// UserID returns the unique identifier for the user.
	UserID() string
	// Claims unmarshalls the user's claims into the provided struct reference.
	Claims(ref any) error
}

type AuthenticationChallenge struct {
	Status          int
	WWWAuthenticate string
}

type Authenticator interface {
	CheckAuthentication(req *http.Request) (AuthenticationResult, error)
}
