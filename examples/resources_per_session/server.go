package resources_per_session

import (
	"context"
	"fmt"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// New constructs a server whose resources vary per session/user.
// Each user sees a single profile resource at res://user/<userID>/profile.
func New() mcpservice.ServerCapabilities {
	resourcesProvider := func(ctx context.Context, s sessions.Session) (mcpservice.ResourcesCapability, bool, error) {
		cap := mcpservice.NewDynamicResources(
			mcpservice.WithResourcesListFunc(func(ctx context.Context, _ sessions.Session, _ *string) (mcpservice.Page[mcp.Resource], error) {
				uri := fmt.Sprintf("res://user/%s/profile", s.UserID())
				return mcpservice.NewPage([]mcp.Resource{{URI: uri, Name: "profile", MimeType: "text/plain"}}), nil
			}),
			mcpservice.WithResourcesReadFunc(func(ctx context.Context, _ sessions.Session, uri string) ([]mcp.ResourceContents, error) {
				return []mcp.ResourceContents{{URI: uri, MimeType: "text/plain", Text: "hello, " + s.UserID()}}, nil
			}),
		)
		return cap, true, nil
	}

	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "examples-resources-per-session", Version: "0.1.0"}),
		mcpservice.WithResourcesProvider(resourcesProvider),
	)
}
