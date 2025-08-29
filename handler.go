package streaminghttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	samplingCapability    hooks.SamplingCapability
	rootsCapability       hooks.RootsCapability
	elicitationCapability hooks.ElicitationCapability
}

func (m *sessionMetadata) ClientInfo() sessions.ClientInfo {
	return m.clientInfo
}

func (m *sessionMetadata) GetSamplingCapability() hooks.SamplingCapability {
	return m.samplingCapability
}

func (m *sessionMetadata) GetRootsCapability() hooks.RootsCapability {
	return m.rootsCapability
}

func (m *sessionMetadata) GetElicitationCapability() hooks.ElicitationCapability {
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
}

// StreamingHTTPHandler implements the stream HTTP transport protocol of
// the Model Context Protocol.
type StreamingHTTPHandler struct {
	mux            *http.ServeMux
	oidcProvider   *oidc.Provider
	prmDocument    wellknown.ProtectedResourceMetadata
	prmDocumentURL *url.URL
	serverURL      *url.URL

	auth     auth.Authenticator
	hooks    hooks.Hooks
	sessions sessions.SessionStore
}

type writeFlusher interface {
	io.Writer
	http.Flusher
}

func NewStreamingHTTPHandler(ctx context.Context, config StreamingHTTPConfig) (*StreamingHTTPHandler, error) {
	mcpURL, err := url.Parse(config.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL %q: %w", config.ServerURL, err)
	}

	if mcpURL.Scheme != "https" && mcpURL.Scheme != "http" {
		return nil, fmt.Errorf("server URL must use HTTP or HTTPS scheme, got %q", mcpURL.Scheme)
	}

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

	h := &StreamingHTTPHandler{
		serverURL:    mcpURL,
		oidcProvider: provider,
		prmDocument: wellknown.ProtectedResourceMetadata{
			Resource:                              mcpURL.String(),
			AuthorizationServers:                  []string{asm.Issuer},
			JwksURI:                               asm.JwksUri,
			ScopesSupported:                       asm.ScopesSupported,
			BearerMethodsSupported:                asm.TokenEndpointAuthMethodsSupported,
			ResourceSigningAlgValuesSupported:     asm.TokenEndpointAuthSigningAlgValuesSupported,
			ResourceName:                          config.ServerName,
			ResourceDocumentation:                 asm.ServiceDocumentation,
			ResourcePolicyURI:                     asm.OpPolicyUri,
			ResourceTosURI:                        asm.OpTosUri,
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
	mux.HandleFunc(fmt.Sprintf("POST %s", strings.TrimPrefix(mcpURL.String(), "https://")), h.handlePostMCP)
	mux.HandleFunc(fmt.Sprintf("GET %s", strings.TrimPrefix(mcpURL.String(), "https://")), h.handleGetMCP)
	mux.HandleFunc(fmt.Sprintf("GET %s", strings.TrimPrefix(h.prmDocumentURL.String(), "https://")), h.handleGetProtectedResourceMetadata)

	h.mux = mux

	return h, nil
}

func (h *StreamingHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handlePostMCP handles the POST /mcp endpoint, which is used by the client to send
// MCP messages to the server and to establish a session.
func (h *StreamingHTTPHandler) handlePostMCP(w http.ResponseWriter, r *http.Request) {
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

	authResult, err := h.auth.CheckAuthentication(r)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !authResult.IsAuthenticated() {
		challenge := authResult.GetAuthenticationChallenge()

		if challenge == nil {
			// Invalid implementation of the interface
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if challenge.WWWAuthenticate != "" {
			w.Header().Add(wwwAuthenticateHeader, challenge.WWWAuthenticate)
		}
		status := http.StatusUnauthorized
		if challenge.Status != 0 {
			status = challenge.Status
		}
		w.WriteHeader(status)
		return
	}

	userInfo, err := authResult.UserInfo()
	if err != nil || userInfo == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var msg jsonrpc.AnyMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	sessID := r.Header.Get(mcpSessionIdHeader)
	if sessID == "" {
		// If the session header is missing, the client MUST be establishing a new session
		// via an initialize MCP request.

		req := msg.AsRequest()
		if req == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.Method != "initialize" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		var initializeReq mcp.InitializeRequest

		if err := json.Unmarshal(req.Params, &initializeReq); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Create session metadata with client info and capability placeholders
		// Note: Client capabilities will be properly implemented by the session layer
		metadata := &sessionMetadata{
			clientInfo: sessions.ClientInfo{
				Name:    initializeReq.ClientInfo.Name,
				Version: initializeReq.ClientInfo.Version,
			},
			// Client capabilities are handled by the session implementation
			// These will be nil if the client doesn't support the capability
			samplingCapability:    nil, // Will be set by session if client supports sampling
			rootsCapability:       nil, // Will be set by session if client supports roots
			elicitationCapability: nil, // Will be set by session if client supports elicitation
		}

		// Check client capabilities and set them if supported
		if initializeReq.Capabilities.Sampling != nil {
			// The session implementation will provide the actual sampling capability
			// For now we set it to nil - this should be handled by the session layer
		}
		if initializeReq.Capabilities.Roots != nil {
			// The session implementation will provide the actual roots capability
			// For now we set it to nil - this should be handled by the session layer
		}
		if initializeReq.Capabilities.Elicitation != nil {
			// The session implementation will provide the actual elicitation capability
			// For now we set it to nil - this should be handled by the session layer
		}

		session, err := h.sessions.CreateSession(ctx, userInfo.UserID(), metadata)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		res, err := h.hooks.Initialize(ctx, session, &initializeReq)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set(mcpSessionIdHeader, session.SessionID())
		w.Header().Set("Content-Type", eventStreamMediaType.String())
		w.WriteHeader(http.StatusOK)
		if err := writeSSEEvent(wf, "response", "", res); err != nil {
			// At this point headers are already written, so we can't change the status code
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

		// This is a request with an ID, so we need to handle it appropriately.
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

	authResult, err := h.auth.CheckAuthentication(r)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !authResult.IsAuthenticated() {
		challenge := authResult.GetAuthenticationChallenge()

		if challenge == nil {
			// Invalid implementation of the interface
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if challenge.WWWAuthenticate != "" {
			w.Header().Add(wwwAuthenticateHeader, challenge.WWWAuthenticate)
		}
		status := http.StatusUnauthorized
		if challenge.Status != 0 {
			status = challenge.Status
		}
		w.WriteHeader(status)
		return
	}

	userInfo, err := authResult.UserInfo()
	if err != nil || userInfo == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	sessionHeader := r.Header.Get(mcpSessionIdHeader)
	if sessionHeader == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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
	// Requests to the GET /mcp endpoint MUST be in the context of an already
	// established session. Session establishment can only happen in POST /mcp.
	// As a result, we can respond with an error in the case of a missing session.
	if err := session.ConsumeMessages(ctx, func(ctx context.Context, env sessions.MessageEnvelope) error {
		var msg jsonrpc.AnyMessage

		if err := json.Unmarshal(env.Message, &msg); err != nil {
			return fmt.Errorf("failed to unmarshal session message: %w", err)
		}

		if env.MessageID != "" {
			if _, err := w.Write([]byte(fmt.Sprintf("id: %s\n", env.MessageID))); err != nil {
				return fmt.Errorf("failed to write session message to http response: %w", err)
			}
		}

		if res := msg.AsRequest(); res != nil {
			msgType := "request"
			if res.ID.IsNil() {
				msgType = "notification"
			}

			if err := writeSSEEvent(wf, msgType, env.MessageID, res); err != nil {
				return fmt.Errorf("failed to write session message to http response: %w", err)
			}
		}

		if res := msg.AsResponse(); res != nil {
			if err := writeSSEEvent(wf, "response", env.MessageID, res); err != nil {
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
	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(h.prmDocument); err != nil {
		http.Error(w, fmt.Sprintf("failed to encode protected resource metadata: %v", err), http.StatusInternalServerError)
		return
	}
}

func (h *StreamingHTTPHandler) handleNotification(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, req *jsonrpc.Request) error {
	// TODO: Actually handle the notification
	return nil
}

func (h *StreamingHTTPHandler) handleRequest(ctx context.Context, session sessions.Session, userInfo auth.UserInfo, req *jsonrpc.Request) (*jsonrpc.Response, error) {
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
	// TODO: Actually handle the response
	return nil
}

// type authenticationResult struct {
// 	isAuthenticated       bool
// 	userInfo              auth.UserInfo
// 	status                int
// 	wwwAuthenticateHeader string
// 	err                   error
// }

// func (h *StreamingHTTPHandler) checkAuthentication(r *http.Request) authenticationResult {
// 	_, cancel := context.WithCancel(r.Context())
// 	defer cancel()

// 	prmDocumentURL := h.prmDocumentURL.String()

// 	if strings.ContainsAny(prmDocumentURL, "\\\"") {
// 		return authenticationResult{
// 			isAuthenticated: false,
// 			status:          http.StatusInternalServerError,
// 			err:             fmt.Errorf("protected resource metadata URL contains invalid characters"),
// 		}
// 	}

// 	authzHeader := r.Header.Get(authorizationHeader)
// 	if authzHeader == "" {
// 		return authenticationResult{
// 			isAuthenticated:       false,
// 			status:                http.StatusUnauthorized,
// 			wwwAuthenticateHeader: fmt.Sprintf("resource_metadata=\"%s\"", prmDocumentURL),
// 		}
// 	}

// 	prefix, value, ok := strings.Cut(authzHeader, " ")
// 	if !ok || prefix != "Bearer" || value == "" {
// 		return authenticationResult{
// 			isAuthenticated: false,
// 			status:          http.StatusBadRequest,
// 		}
// 	}

// 	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
// 	defer cancel()

// 	userInfo, err := h.oidcProvider.UserInfo(ctx, oauth2.StaticTokenSource(&oauth2.Token{
// 		AccessToken: value,
// 	}))
// 	if err != nil {
// 		return authenticationResult{
// 			isAuthenticated: false,
// 			status:          http.StatusInternalServerError,
// 			err:             err,
// 		}
// 	}

// 	// Fallback
// 	return authenticationResult{
// 		isAuthenticated: true,
// 		userInfo:        userInfo,
// 	}
// }

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
