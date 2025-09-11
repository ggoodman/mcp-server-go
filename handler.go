package streaminghttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/elnormous/contenttype"
	"github.com/ggoodman/mcp-streaming-http-go/auth"
	"github.com/ggoodman/mcp-streaming-http-go/hooks"
	"github.com/ggoodman/mcp-streaming-http-go/internal/jsonrpc"
	"github.com/ggoodman/mcp-streaming-http-go/internal/wellknown"
	"github.com/ggoodman/mcp-streaming-http-go/mcp"
	"github.com/ggoodman/mcp-streaming-http-go/sessions"
)

// sessionMetadata implements the sessions.SessionMetadata interface
// to bridge between MCP client capabilities and session requirements
type sessionMetadata struct {
	clientInfo            sessions.ClientInfo
	samplingCapability    sessions.SamplingCapability
	rootsCapability       sessions.RootsCapability
	elicitationCapability sessions.ElicitationCapability
}

func (m *sessionMetadata) ClientInfo() sessions.ClientInfo {
	return m.clientInfo
}

func (m *sessionMetadata) GetSamplingCapability() sessions.SamplingCapability {
	return m.samplingCapability
}

func (m *sessionMetadata) GetRootsCapability() sessions.RootsCapability {
	return m.rootsCapability
}

func (m *sessionMetadata) GetElicitationCapability() sessions.ElicitationCapability {
	return m.elicitationCapability
}

var (
	_ http.Handler = (*StreamingHTTPHandler)(nil)
)

var (
	ErrSessionHeaderMissing = errors.New("missing mcp-session-id header")
	ErrSessionHeaderInvalid = errors.New("invalid mcp-session-id header")
	ErrInvalidSession       = errors.New("invalid mcp session")
)

var (
	jsonMediaType         = contenttype.NewMediaType("application/json")
	eventStreamMediaType  = contenttype.NewMediaType("text/event-stream")
	eventStreamMediaTypes = []contenttype.MediaType{eventStreamMediaType}
)

const (
	lastEventIDHeader     = "last-event-id"
	mcpSessionIdHeader    = "mcp-session-id"
	authorizationHeader   = "authorization"
	wwwAuthenticateHeader = "www-authenticate"
)

type StreamingHTTPConfig struct {
	// ServerURL is the fully qualified URL of the streaming HTTP MCP server
	// endpoint.
	ServerURL string

	ServerName string

	// AuthorizationServerURL is the URL of the OIDC authorization server.
	AuthorizationServerURL string

	// Authenticator provides authentication logic for the MCP server.
	Authenticator auth.Authenticator

	// Hooks provides the MCP server logic implementation.
	Hooks hooks.Hooks

	// SessionHost provides session messaging and revocation. Preferred.
	SessionHost sessions.SessionHost

	// LogHandler is an optional slog.Handler for logging within the handler. If nil, logging is discarded.
	LogHandler slog.Handler
}

// ManualOIDCConfig allows configuring the StreamingHTTPHandler with pre-configured
// OAuth/OIDC details, bypassing the need for OIDC discovery. This is particularly
// useful for testing or environments where OIDC discovery is not available.
type ManualOIDCConfig struct {
	// ServerURL is the fully qualified URL of the streaming HTTP MCP server endpoint.
	ServerURL string

	// ServerName is the human-readable name of the server.
	ServerName string

	// Issuer is the OAuth2 authorization server issuer URL.
	Issuer string

	// JwksURI is the URL of the JSON Web Key Set document.
	JwksURI string

	// Optional fields that would normally be discovered from authorization server metadata
	ScopesSupported                            []string
	TokenEndpointAuthMethodsSupported          []string
	TokenEndpointAuthSigningAlgValuesSupported []string
	ServiceDocumentation                       string
	OpPolicyUri                                string
	OpTosUri                                   string

	// Authenticator provides authentication logic for the MCP server.
	Authenticator auth.Authenticator

	// Hooks provides the MCP server logic implementation.
	Hooks hooks.Hooks

	// SessionHost provides session messaging and revocation. Preferred.
	SessionHost sessions.SessionHost

	// LogHandler is an optional slog.Handler for logging within the handler. If nil, logging is discarded.
	LogHandler slog.Handler
}

// StreamingHTTPHandler implements the stream HTTP transport protocol of
// the Model Context Protocol.
type StreamingHTTPHandler struct {
	mux            *http.ServeMux
	oidcProvider   *oidc.Provider
	log            *slog.Logger
	prmDocument    wellknown.ProtectedResourceMetadata
	prmDocumentURL *url.URL
	serverURL      *url.URL

	auth     auth.Authenticator
	hooks    hooks.Hooks
	sessions sessions.SessionManager
}

type writeFlusher interface {
	io.Writer
	http.Flusher
}

func NewStreamingHTTPHandler(ctx context.Context, config StreamingHTTPConfig) (*StreamingHTTPHandler, error) {
	if config.Authenticator == nil {
		return nil, fmt.Errorf("authenticator is required")
	}

	if config.Hooks == nil {
		return nil, fmt.Errorf("hooks is required")
	}

	// Either Host or Broker must be provided (Host preferred)
	if config.SessionHost == nil {
		return nil, fmt.Errorf("SessionHost is required")
	}

	// Perform OIDC discovery
	provider, err := oidc.NewProvider(ctx, config.AuthorizationServerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	var asm *wellknown.AuthServerMetadata

	if err := provider.Claims(&asm); err != nil {
		return nil, fmt.Errorf("unexpected or invalid authorization server metadata: %w", err)
	}

	if asm.JwksUri == "" {
		return nil, fmt.Errorf("the supplied authorization server does not declare support for a JWKS URI in its metadata")
	}

	// Create manual config from discovered metadata and delegate to manual constructor
	manualConfig := ManualOIDCConfig{
		ServerURL:                         config.ServerURL,
		ServerName:                        config.ServerName,
		Issuer:                            asm.Issuer,
		JwksURI:                           asm.JwksUri,
		ScopesSupported:                   asm.ScopesSupported,
		TokenEndpointAuthMethodsSupported: asm.TokenEndpointAuthMethodsSupported,
		TokenEndpointAuthSigningAlgValuesSupported: asm.TokenEndpointAuthSigningAlgValuesSupported,
		ServiceDocumentation:                       asm.ServiceDocumentation,
		OpPolicyUri:                                asm.OpPolicyUri,
		OpTosUri:                                   asm.OpTosUri,
		Authenticator:                              config.Authenticator,
		Hooks:                                      config.Hooks,
		SessionHost:                                config.SessionHost,
		LogHandler:                                 config.LogHandler,
	}

	handler, err := NewStreamingHTTPHandlerWithManualOIDC(ctx, manualConfig)
	if err != nil {
		return nil, err
	}

	// Set the OIDC provider for the discovery-based path
	handler.oidcProvider = provider

	return handler, nil
}

// NewStreamingHTTPHandlerWithManualOIDC creates a new StreamingHTTPHandler with
// manually provided OAuth/OIDC configuration, bypassing OIDC discovery.
// This is useful for testing or environments where OIDC discovery is not available.
func NewStreamingHTTPHandlerWithManualOIDC(ctx context.Context, config ManualOIDCConfig) (*StreamingHTTPHandler, error) {
	mcpURL, err := url.Parse(config.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", config.ServerURL, err)
	}

	if mcpURL.Scheme != "https" && mcpURL.Scheme != "http" {
		return nil, fmt.Errorf("server URL must use HTTP or HTTPS scheme, got %q", mcpURL.Scheme)
	}

	if config.Issuer == "" {
		return nil, fmt.Errorf("issuer is required")
	}

	if config.JwksURI == "" {
		return nil, fmt.Errorf("JwksURI is required")
	}

	if config.Authenticator == nil {
		return nil, fmt.Errorf("authenticator is required")
	}

	if config.Hooks == nil {
		return nil, fmt.Errorf("hooks is required")
	}

	if config.SessionHost == nil {
		return nil, fmt.Errorf("SessionHost is required")
	}

	logHandler := slog.DiscardHandler

	if config.LogHandler != nil {
		logHandler = config.LogHandler
	}

	log := slog.New(logHandler)
	sessions := sessions.NewManager(config.SessionHost)

	h := &StreamingHTTPHandler{
		log:          log,
		serverURL:    mcpURL,
		oidcProvider: nil, // Not needed for manual configuration
		auth:         config.Authenticator,
		hooks:        config.Hooks,
		sessions:     sessions,
		prmDocument: wellknown.ProtectedResourceMetadata{
			Resource:                              mcpURL.String(),
			AuthorizationServers:                  []string{config.Issuer},
			JwksURI:                               config.JwksURI,
			ScopesSupported:                       config.ScopesSupported,
			BearerMethodsSupported:                config.TokenEndpointAuthMethodsSupported,
			ResourceSigningAlgValuesSupported:     config.TokenEndpointAuthSigningAlgValuesSupported,
			ResourceName:                          config.ServerName,
			ResourceDocumentation:                 config.ServiceDocumentation,
			ResourcePolicyURI:                     config.OpPolicyUri,
			ResourceTosURI:                        config.OpTosUri,
			TlsClientCertificateBoundAccessTokens: false,
			AuthorizationDetailsTypesSupported:    []string{"urn:ietf:params:oauth:authorization-details"},
		},
	}

	// The protected resource URL should be based on the MCP server URL but with
	// the path `.well-known/oauth-protected-resource<suffix>` where `suffix` is the
	// path of the MCP Server itself. We create a new URL to avoid mutating the original.
	h.prmDocumentURL = &url.URL{
		Scheme: mcpURL.Scheme,
		Host:   mcpURL.Host,
		Path:   fmt.Sprintf("/.well-known/oauth-protected-resource%s", mcpURL.Path),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("POST %s", hostPath(mcpURL)), h.handlePostMCP)
	mux.HandleFunc(fmt.Sprintf("GET %s", hostPath(mcpURL)), h.handleGetMCP)
	mux.HandleFunc(fmt.Sprintf("GET %s", hostPath(h.prmDocumentURL)), h.handleGetProtectedResourceMetadata)

	h.mux = mux

	return h, nil
}

// hostPath takes a url.URL and returns the host and path components concatenated.
// For example, for URL "https://example.com:8080/foo/bar", it returns "example.com/foo/bar".
// If the path is empty, it returns
func hostPath(u *url.URL) string {
	path := u.Path
	if path == "" || path == "/" {
		path = "/{$}"
	}
	return u.Hostname() + path
}

func (h *StreamingHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handlePostMCP handles the POST /mcp endpoint, which is used by the client to send
// MCP messages to the server and to establish a session.
func (h *StreamingHTTPHandler) handlePostMCP(w http.ResponseWriter, r *http.Request) {
	// h.log.Debug("handlePostMCP", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	_, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes)
	if err != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	ctype, err := contenttype.GetMediaType(r)
	if err != nil || !ctype.Matches(jsonMediaType) {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	wf, ok := w.(writeFlusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		return
	}

	var msg jsonrpc.AnyMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	sessID := r.Header.Get(mcpSessionIdHeader)
	if sessID == "" {
		// If the session header is missing, the client MUST be establishing a new session
		// via an initialize MCP request.
		if err := h.handleSessionInitialization(ctx, wf, w, userInfo, msg); err != nil {
			// Error handling is done within handleSessionInitialization
			return
		}
		return
	}

	// An MCP-Session-ID header is required for all other requests. The session MUST exist and be
	// bound to the authenticated user.
	session, err := h.sessions.LoadSession(ctx, sessID, userInfo.UserID())
	if err != nil {
		if errors.Is(err, ErrInvalidSession) {
			// Signal that the session is invalid and that the client needs to re-initialize
			// to start a new session.
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// We have a valid session now. Let's see what kind of message it is.
	if req := msg.AsRequest(); req != nil {
		if req.ID.IsNil() {
			if err := h.handleNotification(ctx, session, userInfo, req); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// TODO: Dispatch the notification to the 'right place'.
			w.WriteHeader(http.StatusAccepted)

			switch req.Method {
			case string(mcp.InitializedNotificationMethod):
			}

			return
		}

		// Set the content type for the SSE response
		w.Header().Set("Content-Type", eventStreamMediaType.String())
		w.WriteHeader(http.StatusOK)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		session = newSessionWithWriter(ctx, session, wf)

		// This is a request with an ID, so we need to handle it as a request.
		res, err := h.handleRequest(ctx, session, userInfo, req)
		if err != nil {
			res = &jsonrpc.Response{
				JSONRPCVersion: jsonrpc.ProtocolVersion,
				Error: &jsonrpc.Error{
					Code:    jsonrpc.ErrorCodeInternalError,
					Message: "internal server error",
				},
				ID: req.ID,
			}
		}

		if err := writeSSEEvent(wf, "response", "", res); err != nil {
			// At this point headers are already written, so we can't change the status code
			// TODO: Log the error
			return
		}
	}

	if res := msg.AsResponse(); res != nil {
		// TODO: Dispatch the response to the communication mesh.

		if err := h.handleResponse(ctx, session, userInfo, res); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		return
	}
}

// handleGetMCP handles the GET /mcp endpoint, which is used to consume messages
// from an established session.
func (h *StreamingHTTPHandler) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleGetMCP", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	_, _, err := contenttype.GetAcceptableMediaType(r, eventStreamMediaTypes)
	if err != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}

	wf, ok := w.(writeFlusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	userInfo := h.checkAuthentication(ctx, r, w)
	if userInfo == nil {
		return
	}

	sessionHeader := r.Header.Get(mcpSessionIdHeader)
	if sessionHeader == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	wroteHeadersCh := make(chan struct{})

	session, err := h.sessions.LoadSession(ctx, sessionHeader, userInfo.UserID())
	if err != nil {
		if errors.Is(err, ErrInvalidSession) {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	lastEventID := r.Header.Get(lastEventIDHeader)

	// Requests to the GET /mcp endpoint MUST be in the context of an already
	// established session. Session establishment can only happen in POST /mcp.
	// As a result, we can respond with an error in the case of a missing session.
	if err := session.ConsumeMessages(ctx, lastEventID, func(ctx context.Context, msgID string, bytes []byte) error {
		var msg jsonrpc.AnyMessage

		if err := json.Unmarshal(bytes, &msg); err != nil {
			return fmt.Errorf("failed to unmarshal session message: %w", err)
		}

		if msgID != "" {
			if _, err := w.Write([]byte(fmt.Sprintf("id: %s\n", msgID))); err != nil {
				return fmt.Errorf("failed to write session message to http response: %w", err)
			}
		}

		if res := msg.AsRequest(); res != nil {
			msgType := "request"
			if res.ID.IsNil() {
				msgType = "notification"
			}

			if err := writeSSEEvent(wf, msgType, msgID, res); err != nil {
				return fmt.Errorf("failed to write session message to http response: %w", err)
			}
		}

		if res := msg.AsResponse(); res != nil {
			if err := writeSSEEvent(wf, "response", msgID, res); err != nil {
				return fmt.Errorf("failed to write session message to http response: %w", err)
			}
		}

		return nil
	}); err != nil {
		if errors.Is(err, ErrSessionHeaderMissing) || errors.Is(err, ErrSessionHeaderInvalid) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if errors.Is(err, ErrInvalidSession) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	wf.Flush()

	close(wroteHeadersCh)
}

// handleGetProtectedResourceMetadata serves the OAuth2 Protected Resource Metadata
// document crafted when the StreamingHTTPHandler initialized.
func (h *StreamingHTTPHandler) handleGetProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleGetProtectedResourceMetadata", slog.String("method", r.Method), slog.String("url", r.URL.String()))
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(h.prmDocument); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode protected resource metadata: %v", err), http.StatusInternalServerError)
		return
	}
}

func (h *StreamingHTTPHandler) handleNotification(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, req *jsonrpc.Request) error {
	h.log.Debug("handleNotification", slog.String("method", req.Method), slog.String("user", userInfo.UserID()), slog.String("session", session.SessionID()))
	// TODO: Actually handle the notification
	return nil
}

// handleSessionInitialization handles the initialization of a new MCP session
// when no mcp-session-id header is present in the request.
func (h *StreamingHTTPHandler) handleSessionInitialization(ctx context.Context, wf writeFlusher, w http.ResponseWriter, userInfo auth.UserInfo, msg jsonrpc.AnyMessage) error {
	req := msg.AsRequest()
	if req == nil {
		w.WriteHeader(http.StatusBadRequest)
		return fmt.Errorf("expected request message for session initialization")
	}

	if req.Method != "initialize" {
		w.WriteHeader(http.StatusNotFound)
		return fmt.Errorf("expected initialize method, got %s", req.Method)
	}

	var initializeReq mcp.InitializeRequest
	if err := json.Unmarshal(req.Params, &initializeReq); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return fmt.Errorf("failed to unmarshal initialize request: %w", err)
	}

	opts := []sessions.SessionOption{}

	// Check client capabilities and set them if supported
	if initializeReq.Capabilities.Sampling != nil {
		opts = append(opts, sessions.WithSamplingCapability())
		// The session implementation will provide the actual sampling capability
		// For now we set it to nil - this should be handled by the session layer
	}
	if initializeReq.Capabilities.Roots != nil {
		opts = append(opts, sessions.WithRootsCapability(initializeReq.Capabilities.Roots.ListChanged))
		// The session implementation will provide the actual roots capability
		// For now we set it to nil - this should be handled by the session layer
	}
	if initializeReq.Capabilities.Elicitation != nil {
		opts = append(opts, sessions.WithElicitationCapability())
		// The session implementation will provide the actual elicitation capability
		// For now we set it to nil - this should be handled by the session layer
	}

	session, err := h.sessions.CreateSession(ctx, userInfo.UserID(), opts...)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to create session: %w", err)
	}

	initializeRes, err := h.hooks.Initialize(ctx, session, &initializeReq)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to initialize session: %w", err)
	}

	res, err := jsonrpc.NewResultResponse(req.ID, initializeRes)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("failed to create initialize response: %w", err)
	}

	w.Header().Set(mcpSessionIdHeader, session.SessionID())
	w.Header().Set("Content-Type", eventStreamMediaType.String())
	w.WriteHeader(http.StatusOK)
	if err := writeSSEEvent(wf, "response", "", res); err != nil {
		// At this point headers are already written, so we can't change the status code
		return fmt.Errorf("failed to write SSE event: %w", err)
	}

	return nil
}

// mapHooksErrorToJSONRPCError maps specific hooks errors to appropriate JSON-RPC error responses
func mapHooksErrorToJSONRPCError(requestID *jsonrpc.RequestID, err error) *jsonrpc.Response {
	switch e := err.(type) {
	case *hooks.NotFoundError:
		return jsonrpc.NewErrorResponse(requestID, jsonrpc.ErrorCodeMethodNotFound, e.Error(), nil)
	case *hooks.AlreadySubscribedError, *hooks.NotSubscribedError, *hooks.InvalidParamsError:
		return jsonrpc.NewErrorResponse(requestID, jsonrpc.ErrorCodeInvalidParams, e.Error(), nil)
	case *hooks.UnsupportedOperationError:
		return jsonrpc.NewErrorResponse(requestID, jsonrpc.ErrorCodeMethodNotFound, e.Error(), nil)
	default:
		return jsonrpc.NewErrorResponse(requestID, jsonrpc.ErrorCodeInternalError, "internal error", nil)
	}
}

func (h *StreamingHTTPHandler) handleRequest(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, req *jsonrpc.Request) (*jsonrpc.Response, error) {
	h.log.Debug("handleRequest", slog.String("method", req.Method), slog.String("user", userInfo.UserID()), slog.String("session", session.SessionID()))

	switch req.Method {
	case string(mcp.PingMethod):
		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.ToolsListMethod):
		toolsCap := h.hooks.GetToolsCapability()

		if toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}

		var listToolsReq mcp.ListToolsRequest
		if err := json.Unmarshal(req.Params, &listToolsReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		tools, nextCursor, err := toolsCap.ListTools(ctx, session, listToolsReq.Cursor)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListToolsResult{
			Tools: tools,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: nextCursor,
			},
		})
	case string(mcp.ToolsCallMethod):
		toolsCap := h.hooks.GetToolsCapability()

		if toolsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "tools capability not supported", nil), nil
		}

		var callToolReq mcp.CallToolRequestReceived
		if err := json.Unmarshal(req.Params, &callToolReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		result, err := toolsCap.CallTool(ctx, session, &callToolReq)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, result)
	case string(mcp.ResourcesListMethod):
		resourcesCap := h.hooks.GetResourcesCapability()

		if resourcesCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var listResourcesReq mcp.ListResourcesRequest
		if err := json.Unmarshal(req.Params, &listResourcesReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		resources, nextCursor, err := resourcesCap.ListResources(ctx, session, listResourcesReq.Cursor)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListResourcesResult{
			Resources: resources,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: nextCursor,
			},
		})
	case string(mcp.ResourcesReadMethod):
		resourcesCap := h.hooks.GetResourcesCapability()

		if resourcesCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var readResourceReq mcp.ReadResourceRequest
		if err := json.Unmarshal(req.Params, &readResourceReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		contents, err := resourcesCap.ReadResource(ctx, session, readResourceReq.URI)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ReadResourceResult{
			Contents: contents,
		})
	case string(mcp.ResourcesSubscribeMethod):
		resourcesCap := h.hooks.GetResourcesCapability()

		if resourcesCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var subscribeReq mcp.SubscribeRequest
		if err := json.Unmarshal(req.Params, &subscribeReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		err := resourcesCap.SubscribeToResource(ctx, session, subscribeReq.URI)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.ResourcesUnsubscribeMethod):
		resourcesCap := h.hooks.GetResourcesCapability()

		if resourcesCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "resources capability not supported", nil), nil
		}

		var unsubscribeReq mcp.UnsubscribeRequest
		if err := json.Unmarshal(req.Params, &unsubscribeReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		err := resourcesCap.UnsubscribeFromResource(ctx, session, unsubscribeReq.URI)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.PromptsListMethod):
		promptsCap := h.hooks.GetPromptsCapability()

		if promptsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
		}

		var listPromptsReq mcp.ListPromptsRequest
		if err := json.Unmarshal(req.Params, &listPromptsReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		prompts, nextCursor, err := promptsCap.ListPrompts(ctx, session, listPromptsReq.Cursor)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, &mcp.ListPromptsResult{
			Prompts: prompts,
			PaginatedResult: mcp.PaginatedResult{
				NextCursor: nextCursor,
			},
		})
	case string(mcp.PromptsGetMethod):
		promptsCap := h.hooks.GetPromptsCapability()

		if promptsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "prompts capability not supported", nil), nil
		}

		var getPromptReq mcp.GetPromptRequestReceived
		if err := json.Unmarshal(req.Params, &getPromptReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		result, err := promptsCap.GetPrompt(ctx, session, &getPromptReq)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, result)
	case string(mcp.LoggingSetLevelMethod):
		loggingCap := h.hooks.GetLoggingCapability()

		if loggingCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "logging capability not supported", nil), nil
		}

		var setLevelReq mcp.SetLevelRequest
		if err := json.Unmarshal(req.Params, &setLevelReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		err := loggingCap.SetLevel(ctx, session, setLevelReq.Level)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, struct{}{})
	case string(mcp.CompletionCompleteMethod):
		completionsCap := h.hooks.GetCompletionsCapability()

		if completionsCap == nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeMethodNotFound, "completions capability not supported", nil), nil
		}

		var completeReq mcp.CompleteRequest
		if err := json.Unmarshal(req.Params, &completeReq); err != nil {
			return jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrorCodeInvalidParams, "invalid parameters", nil), nil
		}

		result, err := completionsCap.Complete(ctx, session, &completeReq)
		if err != nil {
			return mapHooksErrorToJSONRPCError(req.ID, err), nil
		}

		return jsonrpc.NewResultResponse(req.ID, result)
	}
	// TODO: Actually handle the request
	return &jsonrpc.Response{
		JSONRPCVersion: jsonrpc.ProtocolVersion,
		Error: &jsonrpc.Error{
			Code:    jsonrpc.ErrorCodeMethodNotFound,
			Message: "method not found",
			Data: map[string]string{
				"method": req.Method,
			},
		},
		ID: req.ID,
	}, nil
}

func (h *StreamingHTTPHandler) handleResponse(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, res *jsonrpc.Response) error {
	h.log.Debug("handleResponse", slog.String("user", userInfo.UserID()), slog.String("session", session.SessionID()))
	// TODO: Actually handle the response
	return nil
}

func (h *StreamingHTTPHandler) checkAuthentication(ctx context.Context, r *http.Request, w http.ResponseWriter) auth.UserInfo {
	authHeader := r.Header.Get(authorizationHeader)

	if authHeader == "" {
		w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="invalid_token", error_description="no token provided"`, h.serverURL.String()))
		w.WriteHeader(http.StatusUnauthorized)
		return nil
	}

	tok, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok || tok == "" {
		w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="invalid_request", error_description="invalid or absent authorization header"`, h.serverURL.String()))
		w.WriteHeader(http.StatusBadRequest)
		return nil
	}

	userInfo, err := h.auth.CheckAuthentication(ctx, tok)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="invalid_token", error_description="%s"`, h.serverURL.String(), err.Error()))
			w.WriteHeader(http.StatusUnauthorized)
			return nil
		}

		if errors.Is(err, auth.ErrInsufficientScope) {
			w.Header().Add(wwwAuthenticateHeader, fmt.Sprintf(`Bearer realm="%s", error="insufficient_scope", error_description="%s"`, h.serverURL.String(), err.Error()))
			w.WriteHeader(http.StatusForbidden)
			return nil
		}

		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}

	return userInfo
}

// writeSSEEvent writes a Server-Sent Event to the response writer with the given event type and message.
// The message will be JSON encoded and written as the data field of the SSE event.
// It automatically flushes the response after writing.
func writeSSEEvent(wf writeFlusher, eventType string, msgID string, message any) error {
	if msgID != "" {
		if _, err := wf.Write([]byte(fmt.Sprintf("id: %s\n", msgID))); err != nil {
			return fmt.Errorf("failed to write SSE event ID: %w", err)
		}
	}

	if _, err := wf.Write([]byte(fmt.Sprintf("event: %s\ndata: ", eventType))); err != nil {
		return fmt.Errorf("failed to write SSE event header: %w", err)
	}

	if err := json.NewEncoder(wf).Encode(message); err != nil {
		return fmt.Errorf("failed to write SSE event data: %w", err)
	}

	if _, err := wf.Write([]byte("\n\n")); err != nil {
		return fmt.Errorf("failed to write SSE event footer: %w", err)
	}

	wf.Flush()
	return nil
}

var _ sessions.Session = (*sessionWithWriter)(nil)

func newSessionWithWriter(ctx context.Context, session sessions.Session, wf writeFlusher) sessions.Session {
	return &sessionWithWriter{
		Session: session,
		reqCtx:  ctx,
		wf:      wf,
		mu:      sync.Mutex{},
	}
}

type sessionWithWriter struct {
	sessions.Session
	reqCtx context.Context
	wf     writeFlusher
	mu     sync.Mutex // Guards concurrent writes to wf
}

func (s *sessionWithWriter) WriteMessage(ctx context.Context, bytes []byte) error {
	// Check the request context has been cancelled. If it has, we should not
	// attempt to write to the response writer as it may be closed.
	// Instead, we fall back to the original session's WriteMessage method.
	// This can happen if the client disconnects while we're trying to write
	// a message to the response.
	if s.reqCtx.Err() != nil {
		// The underlying write flusher should no longer be used so fall back to the original session
		return s.Session.WriteMessage(ctx, bytes)
	}

	var msg jsonrpc.AnyMessage
	if err := json.Unmarshal(bytes, &msg); err != nil {
		// We failed to unmarshal the message. There's no point writing it to the session.
		return err
	}

	s.mu.Lock()
	err := writeSSEEvent(s.wf, msg.Type(), "", bytes)
	s.mu.Unlock()

	if err != nil {
		// TODO: Log error?

		// We failed to write the message to the HTTP response. We'll fall back to writing to the Session.
		return s.Session.WriteMessage(ctx, bytes)
	}

	return nil
}
