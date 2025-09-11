package resources_per_session

import (
	"context"
	"fmt"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpserver"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// New constructs a server whose resources vary per session/user.
// Each user sees a single profile resource at res://user/<userID>/profile.
func New() mcpserver.ServerCapabilities {
	resourcesProvider := func(ctx context.Context, s sessions.Session) (mcpserver.ResourcesCapability, bool, error) {
		cap := mcpserver.NewResourcesCapability(
			mcpserver.WithListResources(func(ctx context.Context, _ sessions.Session, _ *string) (mcpserver.Page[mcp.Resource], error) {
				uri := fmt.Sprintf("res://user/%s/profile", s.UserID())
				return mcpserver.NewPage([]mcp.Resource{{URI: uri, Name: "profile", MimeType: "text/plain"}}), nil
			}),
			mcpserver.WithReadResource(func(ctx context.Context, _ sessions.Session, uri string) ([]mcp.ResourceContents, error) {
				return []mcp.ResourceContents{{URI: uri, MimeType: "text/plain", Text: "hello, " + s.UserID()}}, nil
			}),
		)
		return cap, true, nil
	}

	return mcpserver.NewServer(
		mcpserver.WithServerInfo(mcp.ImplementationInfo{Name: "examples-resources-per-session", Version: "0.1.0"}),
		mcpserver.WithResourcesProvider(resourcesProvider),
	)
}
