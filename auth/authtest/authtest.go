package authtest

import (
	"net/http"

	"github.com/ggoodman/mcp-streaming-http-go/auth"
)

// NoAuth is a test authenticator that always returns authenticated
// Used for testing and development environments where authentication is not required
type NoAuth struct {
	UserID string
}

// NewNoAuth creates a new NoAuth authenticator with the specified user ID
// If userID is empty, it defaults to "test-user"
func NewNoAuth(userID string) *NoAuth {
	if userID == "" {
		userID = "test-user"
	}
	return &NoAuth{UserID: userID}
}

// CheckAuthentication always returns an authenticated result
func (n *NoAuth) CheckAuthentication(req *http.Request) (auth.AuthenticationResult, error) {
	return &noAuthResult{userID: n.UserID}, nil
}

// noAuthResult is a simple implementation that always returns authenticated
type noAuthResult struct {
	userID string
}

func (n *noAuthResult) IsAuthenticated() bool {
	return true
}

func (n *noAuthResult) UserInfo() (auth.UserInfo, error) {
	return &noAuthUserInfo{userID: n.userID}, nil
}

func (n *noAuthResult) GetAuthenticationChallenge() *auth.AuthenticationChallenge {
	return nil
}

// noAuthUserInfo provides user info for the NoAuth authenticator
type noAuthUserInfo struct {
	userID string
}

func (n *noAuthUserInfo) UserID() string {
	return n.userID
}

func (n *noAuthUserInfo) Claims(ref any) error {
	return nil // No claims to unmarshal
}
