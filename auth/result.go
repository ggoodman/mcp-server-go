package auth

import (
	"fmt"
	"net/http"
)

// AuthenticationResult represents the outcome of an authentication attempt.
// Success implementations return non-nil user info; failure variants expose a challenge.
type AuthenticationResult interface {
	UserInfo() (UserInfo, error)
	GetAuthenticationChallenge() *AuthenticationChallenge
}

// AuthenticationChallenge describes an HTTP challenge (status + WWW-Authenticate header).
type AuthenticationChallenge struct {
	Status          int
	WWWAuthenticate string
}

var _ AuthenticationResult = (*authenticationFailure)(nil)

// authenticationFailure represents an authentication failure with appropriate
// challenge information according to the MCP authorization specification
type authenticationFailure struct {
	challenge *AuthenticationChallenge
	err       error
}

// NewAuthenticationRequired builds a challenge indicating credentials are required.
func NewAuthenticationRequired(resourceMetadataURL string) *authenticationFailure {
	return &authenticationFailure{
		challenge: &AuthenticationChallenge{
			Status: http.StatusUnauthorized,
			// TODO: Is there some escaping we need to be doing here?
			WWWAuthenticate: fmt.Sprintf(`Bearer resource_metadata="%s"`, resourceMetadataURL),
		},
	}
}

// NewInvalidAuthorizationHeader builds a challenge for a malformed Authorization header.
func NewInvalidAuthorizationHeader(realm string) *authenticationFailure {
	return &authenticationFailure{
		challenge: &AuthenticationChallenge{
			Status:          http.StatusBadRequest,
			WWWAuthenticate: fmt.Sprintf(`Bearer realm="%s" error="invalid_request", error_description="Invalid Authorization header"`, realm),
		},
	}
}

// NewInvalidTokenResult builds a challenge indicating the token is invalid.
func NewInvalidTokenResult(realm string, description string) *authenticationFailure {
	return &authenticationFailure{
		challenge: &AuthenticationChallenge{
			Status:          http.StatusUnauthorized,
			WWWAuthenticate: fmt.Sprintf(`Bearer realm="%s" error="invalid_token", error_description="%s"`, realm, description),
		},
	}
}

// NewInsufficientScopeResult builds a challenge indicating missing required scope.
func NewInsufficientScopeResult(realm string, scope string) *authenticationFailure {
	return &authenticationFailure{
		challenge: &AuthenticationChallenge{
			Status:          http.StatusUnauthorized,
			WWWAuthenticate: fmt.Sprintf(`Bearer realm="%s" error="insufficient_scope", error_description="Insufficient scope: %s"`, realm, scope),
		},
	}
}

// UserInfo returns an error since authentication failed
func (f *authenticationFailure) UserInfo() (UserInfo, error) {
	return nil, f.err
}

// GetAuthenticationChallenge returns the challenge information for the client
func (f *authenticationFailure) GetAuthenticationChallenge() *AuthenticationChallenge {
	return f.challenge
}
